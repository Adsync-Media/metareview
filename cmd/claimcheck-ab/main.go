// claimcheck-ab runs the issue #146 A/B re-judge: the harnesseval gap-claim corpus
// re-judged TWICE by one judge model —
//
//	arm A ("old"): the #145-era prompt — SystemAdjudicate + the pre-#146 RubricAddendum
//	    (verbatim from git history) + ContextForGapClaim(..., nil)  (diff-only evidence)
//	arm B ("new"): the production prompt — SystemAdjudicate + the current RubricAddendum
//	    + ContextForGapClaim(..., repo)  (diff + repository-head evidence)
//
// and scored against the lab's v2 three-way ground truth. Both arms share one judge
// model and one endpoint, so the delta isolates the #146 change: repository-head
// evidence plus the search-record criterion. Results append to <out>/results.jsonl and
// the run is resumable — completed (key, arm) pairs are skipped on re-invocation.
//
// Usage:
//
//	go run ./cmd/claimcheck-ab -harnesseval ../harnesseval -repos ../harnesseval-repos \
//	  -model glm-5.2-vision -out /tmp/claimcheck-ab -concurrency 6
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dsifry/metareview/internal/claimcheck"
	"github.com/dsifry/metareview/internal/fsm/judge"
	"github.com/dsifry/metareview/internal/fsm/run"
)

// osExit is main's only exit path, var so a test can capture the code instead of
// terminating the test process (the claimcheck-eval pattern).
var osExit = os.Exit

func main() { osExit(realMain()) }

func realMain() int {
	fs := flag.NewFlagSet("claimcheck-ab", flag.ContinueOnError)
	fs.SetOutput(os.Stderr) // a flag misuse must say why, not exit 2 into silence
	var (
		dir         = fs.String("harnesseval", "../harnesseval", "harnesseval checkout (read-only)")
		reposDir    = fs.String("repos", "", "directory of corpus clones (<repos>/<org>/<repo>)")
		model       = fs.String("model", "glm-5.2-vision", "judge model (both arms)")
		effort      = fs.String("effort", "high", "reasoning effort")
		outDir      = fs.String("out", "/tmp/claimcheck-ab", "results directory (resumable)")
		concurrency = fs.Int("concurrency", 6, "parallel judge calls")
		armsFlag    = fs.String("arms", "a,b", "arms to run: a, b, or a,b")
		limit       = fs.Int("limit", 0, "stop after N claims (0 = all)")
	)
	if err := fs.Parse(os.Args[1:]); err != nil {
		return 2
	}
	if *reposDir == "" {
		return fatal(fmt.Errorf("-repos is required (corpus clones)"))
	}
	key, base := os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_BASE_URL")
	if key == "" || base == "" {
		return fatal(fmt.Errorf("OPENAI_API_KEY and OPENAI_BASE_URL are required"))
	}
	arms := map[string]bool{}
	for _, a := range strings.Split(*armsFlag, ",") {
		arms[strings.TrimSpace(a)] = true
	}

	records, diffs, rp := loadCorpus(*dir, *reposDir)
	var claims []abClaim
	withRepo := 0
	seenKey := map[string]bool{}
	for _, rec := range records {
		if _, ok := claimcheck.Detect(rec.IssueText); !ok {
			continue
		}
		diff, ok := diffs[rec.URL]
		if !ok {
			continue
		}
		k := claimKey(rec.URL, rec.IssueText)
		if seenKey[k] {
			continue
		}
		seenKey[k] = true
		if *limit > 0 && len(claims) >= *limit {
			break
		}
		f := run.Finding{File: findingFileFromText(rec.IssueText), IssueText: rec.IssueText}
		var repoEv judge.RepoEvidence
		evCache := filepath.Join(*outDir, "evidence", k+".json")
		if raw, err := os.ReadFile(evCache); err == nil && json.Unmarshal(raw, &repoEv) == nil {
			// cached from a previous pass
		} else if rp.rev(rec.URL) != "" {
			if ev, err := judge.RepoTestEvidence(rp.grepSeamFor(rec.URL), rp.showSeamFor(rec.URL), f, judge.MaxGapEvidenceFiles); err == nil {
				repoEv = ev
				if b, err := json.Marshal(ev); err == nil {
					_ = os.MkdirAll(filepath.Join(*outDir, "evidence"), 0o755)
					_ = os.WriteFile(evCache, b, 0o644)
				}
			}
		}
		if repoEv.Ran && len(repoEv.Evidence) > 0 {
			withRepo++
		}
		claims = append(claims, abClaim{rec: rec, find: f, diff: diff, repoEv: repoEv})
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		return fatal(err)
	}
	fmt.Printf("gap claims to judge: %d (repo evidence found for %d)\n", len(claims), withRepo)

	// resume: skip (key, arm) pairs already recorded without error
	done := map[string]bool{}
	for _, line := range readLines(filepath.Join(*outDir, "results.jsonl")) {
		var r result
		if json.Unmarshal([]byte(line), &r) == nil && r.Error == "" {
			done[r.Key+"|"+r.Arm] = true
		}
	}

	jobs := make(chan abClaim)
	var wg sync.WaitGroup
	for w := 0; w < *concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				for _, arm := range []string{"a", "b"} {
					if !arms[arm] {
						continue
					}
					if done[claimKey(c.rec.URL, c.rec.IssueText)+"|"+arm] {
						continue
					}
					res := judgeOne(env{key: key, base: base, model: *model, effort: *effort}, rp, arm, c)
					appendResult(*outDir, res)
				}
			}
		}()
	}
	for _, c := range claims {
		jobs <- c
	}
	close(jobs)
	wg.Wait()
	report(*outDir, os.Stdout)
	return 0
}

// result is one arm's verdict for one claim.
type result struct {
	Key        string   `json:"key"`
	URL        string   `json:"url"`
	Arm        string   `json:"arm"`
	IssueText  string   `json:"issue_text"`
	V2Verdict  string   `json:"v2_verdict"`
	IsReal     bool     `json:"is_real"`
	Confidence float64  `json:"confidence"`
	Verdict    string   `json:"verdict"` // confirmed | rejected | error
	Reasoning  string   `json:"reasoning,omitempty"`
	Evidence   []string `json:"evidence,omitempty"`
	Error      string   `json:"error,omitempty"`
}

// report prints the per-arm confusion matrix against the v2 ground truth and the
// headline deltas the issue asks about: hallucinated gap-claims confirmed (false
// accepts — the #140/#146 failure mode) and true gap findings rejected (regressions).
// Rows the lab could not adjudicate (v2 "unresolved") are counted but excluded from
// both matrices — they are neither false accepts nor regressions.
func report(outDir string, stdout io.Writer) {
	data, err := os.ReadFile(filepath.Join(outDir, "results.jsonl"))
	if err != nil {
		return
	}
	type row struct {
		arm     string
		v2      string
		verdict string
		err     string
		issue   string
	}
	perArm := map[string][]row{}
	for _, line := range strings.Split(string(data), "\n") {
		var r result
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		perArm[r.Arm] = append(perArm[r.Arm], row{arm: r.Arm, v2: r.V2Verdict, verdict: r.Verdict, err: r.Error, issue: r.IssueText})
	}
	fmt.Fprintln(stdout)
	for _, arm := range []string{"a", "b"} {
		rows := perArm[arm]
		if len(rows) == 0 {
			continue
		}
		title := "arm A (old: diff-only evidence, #145 criterion)"
		if arm == "b" {
			title = "arm B (new: repo-side evidence, #146 criterion)"
		}
		halConf, halRej, halErr := 0, 0, 0
		trueConf, trueRej, trueErr := 0, 0, 0
		unresolved := 0
		for _, r := range rows {
			isHallucination := r.v2 == "hallucination"
			isTrueGap := r.v2 == "bug" || r.v2 == "important_non_bug"
			if r.v2 == "unresolved" {
				unresolved++
			}
			switch {
			case isHallucination && r.verdict == "confirmed":
				halConf++
			case isHallucination && r.verdict == "rejected":
				halRej++
			case isHallucination && r.err != "":
				halErr++
			case isTrueGap && r.verdict == "confirmed":
				trueConf++
			case isTrueGap && r.verdict == "rejected":
				trueRej++
			case isTrueGap && r.err != "":
				trueErr++
			}
		}
		fmt.Fprintf(stdout, "%s\n", title)
		fmt.Fprintf(stdout, "  hallucinated gap-claims: confirmed (FALSE ACCEPTS)=%d rejected=%d errors=%d\n", halConf, halRej, halErr)
		fmt.Fprintf(stdout, "  true gap findings:       confirmed=%d rejected (REGRESSIONS)=%d errors=%d\n", trueConf, trueRej, trueErr)
		fmt.Fprintf(stdout, "  lab-unresolved claims:   %d (excluded from the matrix)\n", unresolved)
	}
	// per-claim paired delta for the claims both arms judged
	paired := map[string][2]string{}
	for arm, rows := range perArm {
		for _, r := range rows {
			p := paired[r.issue]
			if arm == "a" {
				paired[r.issue] = [2]string{r.verdict, p[1]}
			} else {
				paired[r.issue] = [2]string{p[0], r.verdict}
			}
		}
	}
	flips := 0
	for _, v := range paired {
		if v[0] != "" && v[1] != "" && v[0] != v[1] {
			flips++
		}
	}
	fmt.Fprintf(stdout, "\nclaims judged by both arms: %d; verdict flips between arms: %d\n", len(paired), flips)
}

func fatal(err error) int {
	fmt.Fprintln(os.Stderr, "claimcheck-ab:", err)
	return 1
}

func readLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// appendMu serializes result-row appends: each write is a single O_APPEND write call
// (atomic on POSIX for these sizes), but the open-append-close cycle must not race
// with itself or a row can land between another row's open and write.
var appendMu sync.Mutex

func appendResult(outDir string, r *result) {
	appendMu.Lock()
	defer appendMu.Unlock()
	f, err := os.OpenFile(filepath.Join(outDir, "results.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	b, _ := json.Marshal(r)
	_, _ = f.Write(append(b, '\n'))
}
