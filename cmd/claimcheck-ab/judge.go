package main

// judgeOne runs one arm of one gap claim against the judge endpoint.
//
//	arm A ("old"): the #145-era prompt — SystemAdjudicate + the pre-#146 RubricAddendum
//	    (verbatim from git history) + ContextForGapClaim(..., nil)  (diff-only evidence)
//	arm B ("new"): the production prompt — RenderPrompt(KindAdjudicate, ..., calibration=false)
//	    + ContextForGapClaim(..., repo)  (diff + repository-head evidence)
//
// Both arms render the user template identically (fenced), so the delta is exactly the
// #146 change: repository-head evidence plus the search-record criterion. The HTTP call
// replicates the production judge's openai-provider request (judge.prepare's default
// branch): model, system+user messages, max_completion_tokens 16384, reasoning_effort,
// temperature 1.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dsifry/metareview/internal/fsm/judge"
	"github.com/dsifry/metareview/internal/fsm/run"
)

// oldAddendum145 is the pre-#146 RubricAddendum, verbatim from git history
// (628d318:internal/fsm/judge/prompts.go) — the #145-era criterion whose gap-claim
// bullet carries the "the evidence is the diff, not the repository" caveat. Arm A is
// the exact #145-era prompt: this string replaces the current addendum in the system
// prompt, and the context carries no repository evidence.
const oldAddendum145 = "\n\nAdditional criteria for this reviewer (these extend, and where they conflict they override, the instructions above).\nThis review gates a change before it merges; it is not a bug-finding benchmark. A finding is REAL when a maintainer must act on it before merge, which is broader than a wrong computation.\n- An invariant that nothing holds is a real finding. If the finding shows that a guard, check, error-code row or assertion can be deleted or inverted with the test suite still passing, answer is_real true. Working-but-unpinned is the defect: nothing keeps it working. Do not dismiss this as \"only a test-coverage gap\".\n- An assertion that cannot fail is a real finding: a test, subtest or check whose condition is unreachable, tautological, or satisfied by something other than the property it names.\n- A comment, doc line, test name or specification row that asserts a property the code does not have is a real finding, even when the code behaves correctly, because the next reader will rely on it.\n- A finding that claims tests, specs or coverage are ABSENT is real only when the absence is real. When the evidence includes test files whose added lines reference the claimed subject, read them: a test that covers the claimed behavior makes the claim false, however thin the rest of the coverage is. A test that exists yet does not cover the claimed behavior leaves the claim real - cite the gap, not the file count. When the evidence carries NO test file for a subject the diff changes, remember the evidence is the diff, not the repository: absence is established only within what you were shown, and a finding whose claimed subject could plausibly be tested outside the changed files deserves that caveat in your reasoning rather than unqualified confirmation.\nStill answer false for a finding the code contradicts, one that restates intended behaviour, or one whose premise about a tool or language is wrong. If what you were given is not enough to decide, say exactly what was missing in your reasoning rather than guessing."

type env struct{ key, base, model, effort string }

type verdict struct {
	isReal     bool
	confidence float64
	reasoning  string
}

// judgeOne runs one arm of one claim and returns the result row.
func judgeOne(e env, rp *repoPass, arm string, c abClaim) *result {
	ctx := context.Background()
	cand := run.Finding{IssueText: c.find.IssueText, File: c.find.File}
	var system, user string
	if arm == "a" {
		// #145-era: diff-only context; the render's current addendum replaced by the
		// verbatim pre-#146 text. User template and fencing are identical to production.
		contextA, _, _, _ := judge.ContextForGapClaim(c.diff, false, c.find, judge.MaxDiffBytes, nil)
		sysB, userA, _ := judge.RenderPrompt(judge.KindAdjudicate, judge.AdjudicateInput{Diff: contextA, Candidate: cand}, true, false, "claimcheck-ab")
		system = strings.Replace(sysB, judge.RubricAddendum, oldAddendum145, 1)
		user = userA
	} else {
		contextB, _, _, _ := judge.ContextForGapClaim(c.diff, false, c.find, judge.MaxDiffBytes, &c.repoEv)
		sysB, userB, _ := judge.RenderPrompt(judge.KindAdjudicate, judge.AdjudicateInput{Diff: contextB, Candidate: cand}, true, false, "claimcheck-ab")
		system, user = sysB, userB
	}
	fmt.Fprintf(os.Stderr, "[judge] %s arm=%s system=%dB user=%dB\n", claimKey(c.rec.URL, c.rec.IssueText), arm, len(system), len(user))
	v, err := callJudge(ctx, e, system, user)
	fmt.Fprintf(os.Stderr, "[judge] %s arm=%s done err=%v\n", claimKey(c.rec.URL, c.rec.IssueText), arm, err)
	if err != nil {
		return &result{Key: claimKey(c.rec.URL, c.rec.IssueText) + "|" + arm, URL: c.rec.URL, Arm: arm,
			IssueText: c.rec.IssueText, V2Verdict: c.rec.NewVerdict, Verdict: "error", Error: err.Error()}
	}
	vv := "rejected"
	if v.isReal && v.confidence >= 0.7 {
		vv = "confirmed"
	}
	ev := evPaths(c.repoEv)
	return &result{Key: claimKey(c.rec.URL, c.rec.IssueText), URL: c.rec.URL, Arm: arm,
		IssueText: c.rec.IssueText, V2Verdict: c.rec.NewVerdict,
		IsReal: v.isReal, Confidence: v.confidence, Verdict: vv,
		Reasoning: v.reasoning, Evidence: ev}
}

// callJudge posts one chat/completions request and decodes the verdict JSON.
func callJudge(ctx context.Context, e env, system, user string) (verdict, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 300*time.Second)
		defer cancel()
	}
	maxTok := int64(16384)
	body := map[string]any{
		"model": e.model,
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
		"max_completion_tokens": maxTok,
		"reasoning_effort":      e.effort,
		"temperature":           1,
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(e.base, "/")+"/v1/chat/completions", bytes.NewReader(b))
	if err != nil {
		return verdict{}, err
	}
	req.Header.Set("Authorization", "Bearer "+e.key)
	req.Header.Set("content-type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return verdict{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return verdict{}, err
	}
	if resp.StatusCode != 200 {
		return verdict{}, fmt.Errorf("judge endpoint %d: %s", resp.StatusCode, clipBytes(raw, 300))
	}
	var or struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &or); err != nil || len(or.Choices) == 0 {
		return verdict{}, fmt.Errorf("judge body has no choices")
	}
	content := or.Choices[0].Message.Content
	vj := extractJSON(content)
	if vj == "" {
		return verdict{}, fmt.Errorf("no JSON verdict in reply: %s", clipString(content, 200))
	}
	var v struct {
		Reasoning  string  `json:"reasoning"`
		IsReal     bool    `json:"is_real"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(vj), &v); err != nil {
		return verdict{}, fmt.Errorf("verdict is not JSON: %w", err)
	}
	return verdict{isReal: v.IsReal, confidence: v.Confidence, reasoning: v.Reasoning}, nil
}

// extractJSON pulls the first balanced JSON object out of the reply text.
func extractJSON(s string) string {
	start := strings.IndexByte(s, '{')
	if start < 0 {
		return ""
	}
	depth, inStr, esc := 0, false, false
	for i := start; i < len(s); i++ {
		c := s[i]
		if inStr {
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1]
			}
		}
	}
	return ""
}

func clipBytes(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

func clipString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func evPaths(repoEv judge.RepoEvidence) []string {
	var out []string
	for _, r := range repoEv.Evidence {
		out = append(out, r.Path)
	}
	return out
}
