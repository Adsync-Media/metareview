package main

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/dsifry/metareview/internal/claimcheck"
	"github.com/dsifry/metareview/internal/fsm/judge"
	"github.com/dsifry/metareview/internal/fsm/run"
)

// abClaim is one gap claim ready for adjudication, with both arms' inputs resolved.
type abClaim struct {
	rec    record
	find   run.Finding
	diff   string
	repoEv judge.RepoEvidence
}

// record is one readjudication3 row — the lab's v2 three-way ground truth.
type record struct {
	URL        string `json:"url"`
	IssueText  string `json:"issue_text"`
	NewVerdict string `json:"new_verdict"`
}

// repoPass pins a corpus clone per PR URL and exposes the shared judge seams over it.
type repoPass struct {
	revs map[string]string
	dirs map[string]string
}

func (p *repoPass) rev(url string) string { return p.revs[url] }

func runGitRaw(ctx context.Context, dir string, args ...string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Dir = dir
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return out.Bytes(), ee.ExitCode(), nil
		}
		return nil, -1, err
	}
	return out.Bytes(), 0, nil
}

func loadCorpus(dir, reposDir string) ([]record, map[string]string, *repoPass) {
	var records []record
	matches, _ := filepath.Glob(filepath.Join(dir, "runs", "*", "readjudication3.json"))
	for _, m := range matches {
		raw, err := os.ReadFile(m)
		if err != nil {
			continue
		}
		var rj struct {
			URL     string   `json:"url"`
			Records []record `json:"records"`
		}
		if json.Unmarshal(raw, &rj) != nil {
			continue
		}
		for i := range rj.Records {
			rj.Records[i].URL = rj.URL
			records = append(records, rj.Records[i])
		}
	}
	diffs := map[string]string{}
	urls := map[string]bool{}
	for _, r := range records {
		if _, ok := claimcheck.Detect(r.IssueText); !ok {
			continue // only pin repos for gap claims
		}
		urls[r.URL] = true
	}
	for u := range urls {
		sum := sha1.Sum([]byte(u))
		p := filepath.Join(dir, ".cache", "pr_diffs", hex.EncodeToString(sum[:])[:16]+".json")
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var c struct {
			Diff string `json:"diff"`
		}
		if json.Unmarshal(raw, &c) == nil && c.Diff != "" {
			diffs[u] = c.Diff
		}
	}
	rp := &repoPass{revs: map[string]string{}, dirs: map[string]string{}}
	for u := range urls {
		fmt.Fprintf(os.Stderr, "[corpus] fetching %s\n", u)
		m := prURL.FindStringSubmatch(u)
		if m == nil {
			continue
		}
		org, repo, n := m[1], m[2], m[3]
		rd := filepath.Join(reposDir, org, repo)
		if st, err := os.Stat(rd); err != nil || !st.IsDir() {
			continue
		}
		// Resolve (or create) a per-PR ref. refs/heads/pr-N persists across runs, so a
		// previously fetched PR pins locally with no network round trip. Attempt 0 reads
		// the local ref; attempt 1 fetches the PR head shallowly and re-reads it.
		localRef := "refs/heads/pr-" + n
		rev := ""
		for attempt := 0; attempt < 2 && rev == ""; attempt++ {
			if attempt == 1 {
				if _, code, err := runGitRaw(context.Background(), rd, "fetch", "-q", "--depth", "1", "origin", "pull/"+n+"/head:"+localRef); err != nil || code != 0 {
					break
				}
			}
			out, code, err := runGitRaw(context.Background(), rd, "rev-parse", "--verify", localRef+"^{commit}")
			if err == nil && code == 0 {
				rev = strings.TrimSpace(string(out))
			}
		}
		if rev == "" {
			continue
		}
		rp.revs[u], rp.dirs[u] = rev, rd
	}
	return records, diffs, rp
}

// grepSeamFor/showSeamFor delegate to judge.GrepSeam/ShowSeam — the same seams the
// production adjudicator runs, so arm B measures the shipped search.
func (p *repoPass) grepSeamFor(url string) judge.GrepPaths {
	rev, dir := p.revs[url], p.dirs[url]
	if rev == "" {
		return func(string) ([]string, error) { return nil, nil }
	}
	return judge.GrepSeam(context.Background(), runGitRaw, dir, rev)
}

func (p *repoPass) showSeamFor(url string) judge.ShowHead {
	rev, dir := p.revs[url], p.dirs[url]
	if rev == "" {
		return func(string) ([]byte, bool, error) { return nil, false, nil }
	}
	return judge.ShowSeam(context.Background(), runGitRaw, dir, rev)
}

var prURL = regexp.MustCompile(`github\.com/([^/]+)/([^/]+)/pull/(\d+)$`)

var leadingPath = regexp.MustCompile(`^[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+\.[A-Za-z0-9]{1,8}:?`)

func findingFileFromText(text string) string {
	m := leadingPath.FindString(strings.TrimSpace(text))
	return strings.TrimSuffix(m, ":")
}

func claimKey(url, issueText string) string {
	sum := sha1.Sum([]byte(url + "\x00" + issueText))
	return hex.EncodeToString(sum[:])[:16]
}
