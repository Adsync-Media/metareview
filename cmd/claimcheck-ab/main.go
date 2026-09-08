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
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dsifry/metareview/internal/claimcheck"
	"github.com/dsifry/metareview/internal/fsm/judge"
	"github.com/dsifry/metareview/internal/fsm/run"
)

func main() {
	var (
		dir         = flag.String("harnesseval", "../harnesseval", "harnesseval checkout (read-only)")
		reposDir    = flag.String("repos", "", "directory of corpus clones (<repos>/<org>/<repo>)")
		model       = flag.String("model", "glm-5.2-vision", "judge model (both arms)")
		effort      = flag.String("effort", "high", "reasoning effort")
		outDir      = flag.String("out", "/tmp/claimcheck-ab", "results directory (resumable)")
		concurrency = flag.Int("concurrency", 6, "parallel judge calls")
		armsFlag    = flag.String("arms", "a,b", "arms to run: a, b, or a,b")
		limit       = flag.Int("limit", 0, "stop after N claims (0 = all)")
	)
	flag.Parse()
	if *reposDir == "" {
		fatal(fmt.Errorf("-repos is required (corpus clones)"))
	}
	key, base := os.Getenv("OPENAI_API_KEY"), os.Getenv("OPENAI_BASE_URL")
	if key == "" || base == "" {
		fatal(fmt.Errorf("OPENAI_API_KEY and OPENAI_BASE_URL are required"))
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
		fatal(err)
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
	report(*outDir)
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
func report(outDir string) {
	data, err := os.ReadFile(filepath.Join(outDir, "results.jsonl"))
	if err != nil {
		return
	}
	type row struct {
		arm      string
		v2       string
		verdict  string
		err      string
		evidence bool
		issue    string
	}
	perArm := map[string][]row{}
	for _, line := range strings.Split(string(data), "\n") {
		var r result
		if json.Unmarshal([]byte(line), &r) != nil {
			continue
		}
		perArm[r.Arm] = append(perArm[r.Arm], row{arm: r.Arm, v2: r.V2Verdict, verdict: r.Verdict, err: r.Error, evidence: len(r.Evidence) > 0, issue: r.IssueText})
	}
	fmt.Println()
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
		fmt.Printf("%s\n", title)
		fmt.Printf("  hallucinated gap-claims: confirmed (FALSE ACCEPTS)=%d rejected=%d errors=%d\n", halConf, halRej, halErr)
		fmt.Printf("  true gap findings:       confirmed=%d rejected (REGRESSIONS)=%d errors=%d\n", trueConf, trueRej, trueErr)
		fmt.Printf("  lab-unresolved claims:   %d (excluded from the matrix)\n", unresolved)
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
	fmt.Printf("\nclaims judged by both arms: %d; verdict flips between arms: %d\n", len(paired), flips)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "claimcheck-ab:", err)
	os.Exit(1)
}

func readLines(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Split(string(data), "\n")
}

func appendResult(outDir string, r *result) {
	f, err := os.OpenFile(filepath.Join(outDir, "results.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer func() { _ = f.Close() }()
	b, _ := json.Marshal(r)
	_, _ = f.Write(append(b, '\n'))
}
