package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dsifry/metareview/internal/claimcheck"
	"github.com/dsifry/metareview/internal/fsm/judge"
	"github.com/dsifry/metareview/internal/fsm/run"
)

// --- fixture helpers ---------------------------------------------------------

// gapText is a testing-gap claim the detector selects; it names a file so
// findingFileFromText and the subject-token grep both have something to work with.
const gapText = "app/models/topic_embed.rb:36 — No test verifies that the embed path works"

// initRepo creates a git repo at dir with one commit containing the given files.
func initRepo(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", ".")
	}
	git("commit", "-q", "-m", "c1")
}

// buildLab writes a miniature harnesseval: records, cached diffs, and corpus clones.
// It returns the lab dir, the repos dir, and the pinned-rev fetch that already ran.
func buildLab(t *testing.T) (lab, repos string) {
	t.Helper()
	lab = t.TempDir()
	repos = t.TempDir()

	write := func(rel, content string) {
		p := filepath.Join(lab, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// run1: the claim with a cloneable PR, its non-claim sibling, a claim whose diff is
	// missing, one with a corrupt diff, a claim on a non-GitHub URL, and a duplicate.
	records := []map[string]any{
		{"issue_text": gapText, "new_verdict": "bug"},
		{"issue_text": "nil dereference in the embed path", "new_verdict": "hallucination"},
		{"issue_text": "zero test coverage for the scheduler", "new_verdict": "important_non_bug"},
		{"issue_text": "lacks a spec for the retry loop", "new_verdict": "unresolved"},
		{"issue_text": "nothing asserts the rate limit", "new_verdict": "bug"},
		{"issue_text": gapText, "new_verdict": "bug"}, // duplicate of the first
	}
	urls := []string{
		"https://github.com/org/repo/pull/1",
		"https://github.com/org/repo/pull/1",
		"https://github.com/org/repo/pull/2", // no cached diff
		"https://github.com/org/repo/pull/3", // corrupt diff cache
		"https://example.com/other",          // not a GitHub PR URL
		"https://github.com/org/repo/pull/1",
	}
	for i := range records {
		records[i]["url"] = urls[i]
	}
	raw, _ := json.Marshal(map[string]any{"url": urls[0], "records": records})
	write(filepath.Join("runs", "run1", "readjudication3.json"), string(raw))
	write(filepath.Join("runs", "run2", "readjudication3.json"), "{not json")

	for u, diff := range map[string]string{
		"https://github.com/org/repo/pull/1": "--- a/app/models/topic_embed.rb\n+++ b/app/models/topic_embed.rb\n@@ -1 +1 @@\n-def embed\n+def embed(x)\n",
		"https://example.com/other":          "--- a/x.rb\n+++ b/x.rb\n@@ -1 +1 @@\n",
		"https://github.com/org/repo/pull/3": "{not json",
	} {
		sum := sha1hex(u)
		write(filepath.Join(".cache", "pr_diffs", sum+".json"), `{"diff":`+jsonQuote(diff)+`}`)
	}

	// repos/org/repo: a clone with a local origin carrying refs/pull/1/head, and a
	// covering test file so the repo-side evidence search finds a candidate.
	origin := filepath.Join(t.TempDir(), "origin")
	initRepo(t, origin, map[string]string{
		"app/models/topic_embed.rb": "def embed(x); x; end\n",
	})
	if out, err := exec.Command("git", "-C", origin, "rev-parse", "HEAD").Output(); err != nil {
		t.Fatal(err)
	} else if err := exec.Command("git", "-C", origin, "update-ref", "refs/pull/1/head", strings.TrimSpace(string(out))).Run(); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(repos, "org", "repo")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	gitc := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = clone
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	gitc("init", "-q")
	gitc("remote", "add", "origin", origin)
	gitc("fetch", "-q", "--depth", "1", "origin", "pull/1/head:refs/heads/pr-1")
	initRepo(t, filepath.Join(repos, "org2", "repo2"), map[string]string{"x.rb": "x\n"}) // no remote → fetch fails
	// repos/org3/repo3 deliberately missing → the URL for it is never pinned.
	return lab, repos
}

func sha1hex(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// judgeHandler returns an httptest server speaking chat/completions; it records the
// system prompts it was sent per arm for the A/B delta assertion.
func judgeHandler(t *testing.T, isReal bool, conf float64) (*httptest.Server, *syncBuffer) {
	t.Helper()
	got := &syncBuffer{mu: &sync.Mutex{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got.mu.Lock()
		got.data += string(body) + "\n"
		got.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": `{"reasoning":"r","is_real":` + jsonBool(isReal) + `,"confidence":` + jsonFloat(conf) + `}`}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, got
}

type syncBuffer struct {
	mu   *sync.Mutex
	data string
}

// --- unit tests --------------------------------------------------------------

func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", `x{"a":1}y`, `{"a":1}`},
		{"nested", `{"a":{"b":2}}`, `{"a":{"b":2}}`},
		{"escaped quote", `{"a":"he said \"hi\" ok"}`, `{"a":"he said \"hi\" ok"}`},
		{"escaped backslash", `{"a":"c:\\path"}`, `{"a":"c:\\path"}`},
		{"no brace", "no json here", ""},
		{"unclosed", `{"a":1`, ""},
		{"no opening", "}", ""},
	}
	for _, tc := range cases {
		if got := extractJSON(tc.in); got != tc.want {
			t.Errorf("%s: extractJSON = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestClipAndHelpers(t *testing.T) {
	if got := clipBytes([]byte("abcdef"), 3); got != "abc…" {
		t.Errorf("clipBytes long = %q", got)
	}
	if got := clipBytes([]byte("ab"), 3); got != "ab" {
		t.Errorf("clipBytes short = %q", got)
	}
	if got := clipString("abcdef", 3); got != "abc…" {
		t.Errorf("clipString long = %q", got)
	}
	if got := clipString("ab", 3); got != "ab" {
		t.Errorf("clipString short = %q", got)
	}
	if got := findingFileFromText(gapText); got != "app/models/topic_embed.rb" {
		t.Errorf("findingFileFromText with path = %q", got)
	}
	if got := findingFileFromText("app/models/topic_embed.rb: no test"); got != "app/models/topic_embed.rb" {
		t.Errorf("findingFileFromText with colon = %q", got)
	}
	if got := findingFileFromText("no path here at all"); got != "" {
		t.Errorf("findingFileFromText no match = %q", got)
	}
	if claimKey("u", "t") == "" || claimKey("u", "t") != claimKey("u", "t") || claimKey("u", "t") == claimKey("u2", "t") {
		t.Error("claimKey must be deterministic and input-sensitive")
	}
	ev := evPaths(judge.RepoEvidence{Evidence: []claimcheck.Evidence{{Path: "test/a_test.rb"}, {Path: "spec/b_spec.rb"}}})
	if len(ev) != 2 || ev[0] != "test/a_test.rb" {
		t.Errorf("evPaths = %v", ev)
	}
	if got := evPaths(judge.RepoEvidence{}); got != nil {
		t.Errorf("evPaths empty = %v", got)
	}
}

func TestRunGitRaw(t *testing.T) {
	dir := t.TempDir()
	if out, code, err := runGitRaw(context.Background(), dir, "init", "-q"); err != nil || code != 0 || len(out) != 0 {
		t.Fatalf("git init: code=%d err=%v out=%q", code, err, out)
	}
	if _, code, err := runGitRaw(context.Background(), dir, "rev-parse", "--verify", "definitely-not-a-ref"); err != nil || code == 0 {
		t.Fatalf("failing git: code=%d err=%v (want exit code, nil err)", code, err)
	}
	if _, code, err := runGitRaw(context.Background(), filepath.Join(dir, "missing"), "status"); err == nil || code != -1 {
		t.Fatalf("start failure: code=%d err=%v (want -1, err)", code, err)
	}
}

func TestRepoPassSeams(t *testing.T) {
	lab, repos := buildLab(t)
	records, diffs, rp := loadCorpus(lab, repos)
	if len(records) == 0 || len(diffs) == 0 {
		t.Fatalf("corpus load: %d records, %d diffs", len(records), len(diffs))
	}
	url1 := "https://github.com/org/repo/pull/1"
	if rp.rev(url1) == "" {
		t.Fatal("pull/1 must be pinned from the local origin")
	}
	paths, err := rp.grepSeamFor(url1)("topic_embed")
	if err != nil {
		t.Fatalf("grepSeamFor: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("the covering test file must be recalled by the subject grep")
	}
	if _, ok, err := rp.showSeamFor(url1)(paths[0]); err != nil || !ok {
		t.Fatalf("showSeamFor(%q): ok=%v err=%v", paths[0], ok, err)
	}
	// an unpinned URL yields no-op seams
	dead := "https://github.com/org3/repo3/pull/8"
	if rp.rev(dead) != "" {
		t.Fatal("repo3 must not be pinned (its dir does not exist)")
	}
	if p, err := rp.grepSeamFor(dead)("x"); err != nil || p != nil {
		t.Errorf("dead grepSeamFor = %v, %v", p, err)
	}
	if b, ok, err := rp.showSeamFor(dead)("x"); err != nil || ok || b != nil {
		t.Errorf("dead showSeamFor = %v, %v, %v", b, ok, err)
	}
	// re-load: the pinned ref must resolve without re-fetching
	_, _, rp2 := loadCorpus(lab, repos)
	if rp2.rev(url1) != rp.rev(url1) {
		t.Error("re-pinning must resolve to the same rev")
	}
}

func TestCallJudgeVerdicts(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		isReal  bool
		wantErr string
	}{
		{"confirmed", 200, "", true, ""},
		{"rejected low confidence", 200, "", false, ""},
		{"http error", 500, "boom", false, "judge endpoint 500"},
		{"no choices", 200, `{"choices":[]}`, false, "no choices"},
		{"bad body", 200, `<html>`, false, "no choices"},
		{"no verdict json", 200, `{"choices":[{"message":{"content":"no braces"}}]}`, false, "no JSON verdict"},
		{"verdict not json", 200, `{"choices":[{"message":{"content":"{\"is_real\":\"yes\"}"}}]}`, false, "verdict is not JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				if tc.body != "" {
					_, _ = w.Write([]byte(tc.body))
					return
				}
				content := `{"reasoning":"r","is_real":` + jsonBool(tc.isReal) + `,"confidence":0.95}`
				_ = json.NewEncoder(w).Encode(map[string]any{
					"choices": []map[string]any{{"message": map[string]any{"content": content}}},
				})
			}))
			t.Cleanup(srv.Close)
			v, err := callJudge(context.Background(), env{base: srv.URL}, "s", "u")
			switch {
			case tc.wantErr == "" && err != nil:
				t.Errorf("want success, got %v", err)
			case tc.wantErr == "" && (v.isReal != tc.isReal || v.confidence != 0.95):
				t.Errorf("verdict = %+v, want is_real=%v", v, tc.isReal)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Errorf("want error %q, got %v", tc.wantErr, err)
			}
		})
	}
	// success content is assembled by the handler for the two 200-with-verdict cases
	for _, isReal := range []bool{true, false} {
		srv, err := startVerdictServer(isReal, 0.95)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(srv.Close)
		v, err := callJudge(context.Background(), env{base: srv.URL}, "s", "u")
		if err != nil {
			t.Fatalf("is_real=%v: %v", isReal, err)
		}
		if v.isReal != isReal || v.confidence != 0.95 {
			t.Errorf("is_real=%v: verdict = %+v", isReal, v)
		}
	}
	// connection refused
	if _, err := callJudge(context.Background(), env{base: "http://127.0.0.1:1"}, "s", "u"); err == nil {
		t.Error("unreachable endpoint must error")
	}
	// unparseable URL
	if _, err := callJudge(context.Background(), env{base: "ht tp://bad"}, "s", "u"); err == nil {
		t.Error("bad URL must error")
	}
	// a caller-supplied deadline is honored, not overwritten
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	srv2, err := startVerdictServer(true, 0.9)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(srv2.Close)
	if _, err := callJudge(ctx, env{base: srv2.URL}, "s", "u"); err != nil {
		t.Errorf("deadline ctx: %v", err)
	}
}

func startVerdictServer(isReal bool, conf float64) (*httptest.Server, error) {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		content := `{"reasoning":"r","is_real":` + jsonBool(isReal) + `,"confidence":` + jsonFloat(conf) + `}`
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		})
	})), nil
}

func TestJudgeOneArms(t *testing.T) {
	srv, got := judgeHandler(t, true, 0.9)
	lab, repos := buildLab(t)
	_, _, rp := loadCorpus(lab, repos)
	url1 := "https://github.com/org/repo/pull/1"
	rec := record{URL: url1, IssueText: gapText, NewVerdict: "bug"}
	c := abClaim{
		rec:  rec,
		find: run.Finding{File: "app/models/topic_embed.rb", IssueText: gapText},
		diff: "--- a/app/models/topic_embed.rb\n+++ b/app/models/topic_embed.rb\n",
	}
	c.repoEv.Ran = true
	e := env{base: srv.URL, model: "test-model", effort: "low"}
	for _, arm := range []string{"a", "b"} {
		res := judgeOne(e, rp, arm, c)
		if res.Error != "" || res.Verdict != "confirmed" || res.Confidence != 0.9 || !res.IsReal {
			t.Fatalf("arm %s: %+v", arm, res)
		}
		if res.Key != claimKey(url1, gapText) || res.V2Verdict != "bug" {
			t.Errorf("arm %s: key/verdict mismatch: %+v", arm, res)
		}
	}
	// the A/B delta is the point of the tool: arm A's system prompt must carry the
	// #145-era diff-only caveat and NOT the #146 repo-search criterion; arm B the reverse.
	sysA := systemFor(t, got.data, url1, "a")
	sysB := systemFor(t, got.data, url1, "b")
	if !strings.Contains(sysA, "the evidence is the diff, not the repository") {
		t.Error("arm A must carry the #145-era diff-only caveat")
	}
	if strings.Contains(sysA, "repository-head candidates") {
		t.Error("arm A must NOT carry the #146 repo-search criterion")
	}
	if !strings.Contains(sysB, "repository-head candidates") {
		t.Error("arm B must carry the #146 repo-search criterion")
	}
	if strings.Contains(sysB, "the evidence is the diff, not the repository") {
		t.Error("arm B must NOT carry the #145-era caveat")
	}
	// error path: the endpoint is gone
	srv.Close()
	res := judgeOne(e, rp, "a", c)
	if res.Verdict != "error" || res.Error == "" || !strings.HasSuffix(res.Key, "|a") {
		t.Errorf("error row = %+v", res)
	}
}

// systemFor pulls the system message for one (url, arm) request out of the recorded
// raw request bodies.
func systemFor(t *testing.T, raw, url, arm string) string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if json.Unmarshal([]byte(line), &req) != nil {
			continue
		}
		if len(req.Messages) == 2 && strings.Contains(req.Messages[1].Content, url) && strings.Contains(req.Messages[1].Content, armMarker(arm)) {
			return req.Messages[0].Content
		}
	}
	t.Fatalf("no recorded request for %s/%s", url, arm)
	return ""
}

// armMarker: the user prompt embeds the claim text; the arm is distinguishable by the
// system-prompt addendum, but the request body does not name the arm — so instead the
// marker is the known prompt-size difference. We match on the user content containing
// the claim URL and rely on request order (a then b per claim).
func armMarker(arm string) string {
	if arm == "a" {
		return "the evidence is the diff, not the repository"
	}
	return "repository-head candidates"
}

func TestReportMatrix(t *testing.T) {
	out := t.TempDir()
	rows := []result{
		{Key: "k1", Arm: "a", IssueText: "h1", V2Verdict: "hallucination", Verdict: "confirmed"},
		{Key: "k2", Arm: "a", IssueText: "h2", V2Verdict: "hallucination", Verdict: "rejected"},
		{Key: "k3", Arm: "a", IssueText: "h3", V2Verdict: "hallucination", Verdict: "error", Error: "x"},
		{Key: "k4", Arm: "a", IssueText: "t1", V2Verdict: "bug", Verdict: "confirmed"},
		{Key: "k5", Arm: "a", IssueText: "t2", V2Verdict: "important_non_bug", Verdict: "rejected"},
		{Key: "k6", Arm: "a", IssueText: "t3", V2Verdict: "bug", Verdict: "error", Error: "x"},
		{Key: "k7", Arm: "a", IssueText: "u1", V2Verdict: "unresolved", Verdict: "rejected"},
		{Key: "k4b", Arm: "b", IssueText: "t1", V2Verdict: "bug", Verdict: "rejected"}, // flip
		{Key: "k1b", Arm: "b", IssueText: "h1", V2Verdict: "hallucination", Verdict: "confirmed"},
	}
	for _, r := range rows {
		appendResult(out, &r)
	}
	got := captureStdout(t, func() { report(out, os.Stdout) })
	for _, want := range []string{
		"arm A (old: diff-only evidence, #145 criterion)",
		"arm B (new: repo-side evidence, #146 criterion)",
		"confirmed (FALSE ACCEPTS)=1 rejected=1 errors=1",
		"confirmed=1 rejected (REGRESSIONS)=1 errors=1",
		"lab-unresolved claims:   1 (excluded from the matrix)",
		"claims judged by both arms: 7; verdict flips between arms: 1",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("report missing %q:\n%s", want, got)
		}
	}
	// an arm with no rows is skipped without a nil map deref
	out2 := t.TempDir()
	appendResult(out2, &result{Key: "k1", Arm: "a", IssueText: "h1", V2Verdict: "hallucination", Verdict: "confirmed"})
	got2 := captureStdout(t, func() { report(out2, os.Stdout) })
	if !strings.Contains(got2, "arm A") || strings.Contains(got2, "arm B") {
		t.Errorf("single-arm report wrong:\n%s", got2)
	}
	// a missing results file is silent
	captureStdout(t, func() { report(t.TempDir(), os.Stdout) })
}

func TestAppendReadHelpers(t *testing.T) {
	out := t.TempDir()
	appendResult(out, &result{Key: "k", Arm: "a", Verdict: "confirmed"})
	if lines := readLines(filepath.Join(out, "results.jsonl")); len(lines) != 1 {
		t.Errorf("readLines = %d lines, want 1", len(lines))
	}
	if readLines(filepath.Join(out, "nope")) != nil {
		t.Error("missing file must read as nil")
	}
	// appending to an out-dir that is a file is a silent no-op
	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	appendResult(blocked, &result{Key: "k", Arm: "a"})
}

// --- end-to-end through realMain ---------------------------------------------

func TestRealMainEndToEnd(t *testing.T) {
	lab, repos := buildLab(t)
	srv, got := judgeHandler(t, false, 0.9) // everything rejected: exercises the low-confidence branch
	out := t.TempDir()
	// pre-seed the evidence cache for the pinned claim (cache-hit branch) and a done row
	// (resume-skip branch).
	preKey := claimKey("https://github.com/org/repo/pull/1", gapText)
	if err := os.MkdirAll(filepath.Join(out, "evidence"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "evidence", preKey+".json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	appendResult(out, &result{Key: preKey, Arm: "a", V2Verdict: "bug", Verdict: "rejected"})
	t.Setenv("OPENAI_API_KEY", "k")
	t.Setenv("OPENAI_BASE_URL", srv.URL)
	setArgs(t, "claimcheck-ab", "-harnesseval", lab, "-repos", repos, "-out", out,
		"-concurrency", "2", "-arms", "a,b")
	if code := realMain(); code != 0 {
		t.Fatalf("realMain = %d, want 0", code)
	}
	// arm A of the pre-seeded claim was skipped: only the remaining pairs called the judge
	calls := strings.Count(strings.TrimSpace(got.data), "\n") + boolToInt(len(got.data) > 0)
	if calls < 4 { // 5 claims × 2 arms − 1 skipped ≈ 9; loose floor guards fixture drift
		t.Errorf("judge calls recorded = %d, want the non-resumed pairs to have called", calls)
	}
	if _, err := os.Stat(filepath.Join(out, "evidence", preKey+".json")); err != nil {
		t.Error("evidence cache must persist")
	}
	// a second run resumes and makes no new judge calls
	before := judgeCalls(got)
	if code := realMain(); code != 0 {
		t.Fatalf("resume realMain = %d", code)
	}
	if after := judgeCalls(got); after != before {
		t.Errorf("resume made %d new judge calls, want 0", after-before)
	}
}

func TestRealMainFailurePaths(t *testing.T) {
	lab, repos := buildLab(t)

	t.Run("flag misuse exits 2", func(t *testing.T) {
		setArgs(t, "claimcheck-ab", "-nope")
		if code := realMain(); code != 2 {
			t.Errorf("code = %d, want 2", code)
		}
	})
	t.Run("missing repos flag", func(t *testing.T) {
		setArgs(t, "claimcheck-ab")
		if code := realMain(); code != 1 {
			t.Errorf("code = %d, want 1", code)
		}
	})
	t.Run("missing env", func(t *testing.T) {
		t.Setenv("OPENAI_API_KEY", "")
		t.Setenv("OPENAI_BASE_URL", "")
		setArgs(t, "claimcheck-ab", "-repos", repos)
		if code := realMain(); code != 1 {
			t.Errorf("code = %d, want 1", code)
		}
	})
	t.Run("out dir is a file", func(t *testing.T) {
		blocked := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Setenv("OPENAI_API_KEY", "k")
		t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1")
		setArgs(t, "claimcheck-ab", "-harnesseval", lab, "-repos", repos, "-out", blocked)
		if code := realMain(); code != 1 {
			t.Errorf("code = %d, want 1", code)
		}
	})
	t.Run("main wrapper exits with realMain's code", func(t *testing.T) {
		exitCode := -1
		orig := osExit
		osExit = func(c int) { exitCode = c }
		defer func() { osExit = orig }()
		t.Setenv("OPENAI_API_KEY", "")
		t.Setenv("OPENAI_BASE_URL", "")
		setArgs(t, "claimcheck-ab", "-repos", repos)
		main()
		if exitCode != 1 {
			t.Errorf("osExit got %d, want 1 (missing env)", exitCode)
		}
	})
}

// --- small helpers -----------------------------------------------------------

func setArgs(t *testing.T, args ...string) {
	t.Helper()
	orig := os.Args
	os.Args = args
	t.Cleanup(func() { os.Args = orig })
}

func captureStdout(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	f()
	os.Stdout = orig
	_ = w.Close()
	data, _ := io.ReadAll(r)
	return string(data)
}

func judgeCalls(got *syncBuffer) int {
	got.mu.Lock()
	defer got.mu.Unlock()
	n := 0
	for _, line := range strings.Split(got.data, "\n") {
		if strings.Contains(line, `"model"`) {
			n++
		}
	}
	return n
}

func jsonBool(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func jsonFloat(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
