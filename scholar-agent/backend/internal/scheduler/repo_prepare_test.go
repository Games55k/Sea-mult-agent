package scheduler

import (
	"encoding/json"
	"testing"

	"scholar-agent-backend/internal/models"
)

func TestRepoPrepareCandidateURLs_NormalizesAndAddsFallbacks(t *testing.T) {
	candidates, _ := json.Marshal([]repoCandidate{
		{
			RepoName: "brandokoch/attention-is-all-you-need-paper",
			RepoURLs: []string{
				"https://github.com/brandokoch/attention-is-all-you-need-paper",
			},
		},
		{
			RepoName: "example/transformer",
			RepoURLs: []string{
				"https://github.com/example/transformer.git",
			},
		},
	})
	task := &models.Task{
		Description: "Global user intent: reproduce Attention Is All You Need",
		Inputs: map[string]any{
			"candidate_repositories": string(candidates),
		},
	}

	urls := repoPrepareCandidateURLs(task, "https://github.com/brandokoch/attention-is-all-you-need-paper.git")
	expected := []string{
		"https://github.com/brandokoch/attention-is-all-you-need-paper",
		"https://github.com/harvardnlp/annotated-transformer",
		"https://github.com/example/transformer",
	}
	if len(urls) != len(expected) {
		t.Fatalf("expected %d urls, got %d: %#v", len(expected), len(urls), urls)
	}
	for i := range expected {
		if urls[i] != expected[i] {
			t.Fatalf("url[%d]: expected %q, got %q", i, expected[i], urls[i])
		}
	}
}
