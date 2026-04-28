package scheduler

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"scholar-agent-backend/internal/models"
)

type repoPrepareManifest struct {
	RepoURL            string   `json:"repo_url"`
	WorkspacePath      string   `json:"workspace_path"`
	SelectedCodeFile   string   `json:"selected_code_file,omitempty"`
	DependencyFiles    []string `json:"dependency_files,omitempty"`
	CodeFileCandidates []string `json:"code_file_candidates,omitempty"`
	CloneAttempts      []string `json:"clone_attempts,omitempty"`
}

func executeRepoPrepare(ctx context.Context, runtimeTask *models.Task) error {
	if runtimeTask == nil {
		return fmt.Errorf("runtime task is nil")
	}

	repoURL := strings.TrimSpace(taskInputValue(runtimeTask, "repo_url"))
	if repoURL == "" {
		return fmt.Errorf("repo_prepare: missing repo_url")
	}

	candidateURLs := repoPrepareCandidateURLs(runtimeTask, repoURL)
	repoURL, workspacePath, cloneAttempts, err := cloneFirstAvailableRepository(ctx, candidateURLs)
	if err != nil {
		return err
	}

	dependencyFiles, codeCandidates, scanErr := scanRepositoryWorkspace(workspacePath)
	if scanErr != nil {
		return scanErr
	}

	selectedCodeFile := choosePreferredCodeFile(codeCandidates)
	generatedCode := ""
	if selectedCodeFile != "" {
		raw, readErr := os.ReadFile(selectedCodeFile)
		if readErr != nil {
			return fmt.Errorf("read selected repo code file failed: %w", readErr)
		}
		generatedCode = string(raw)
	}

	manifest := repoPrepareManifest{
		RepoURL:            repoURL,
		WorkspacePath:      workspacePath,
		SelectedCodeFile:   selectedCodeFile,
		DependencyFiles:    toWorkspaceRelativePaths(workspacePath, dependencyFiles),
		CodeFileCandidates: toWorkspaceRelativePaths(workspacePath, codeCandidates),
		CloneAttempts:      cloneAttempts,
	}
	manifestJSON, _ := json.Marshal(manifest)

	if runtimeTask.Metadata == nil {
		runtimeTask.Metadata = map[string]any{}
	}
	runtimeTask.Metadata["artifact_values"] = map[string]any{
		"workspace_path": workspacePath,
		"code_file_path": selectedCodeFile,
		"generated_code": generatedCode,
		"repo_manifest":  string(manifestJSON),
	}

	runtimeTask.Result = chooseNonEmpty(workspacePath, selectedCodeFile, repoURL)
	runtimeTask.Code = generatedCode
	runtimeTask.Status = models.StatusCompleted
	return nil
}

func repoPrepareCandidateURLs(task *models.Task, primary string) []string {
	urls := make([]string, 0, 6)
	appendURL := func(value string) {
		value = normalizeGitHubRepoURL(value)
		if value == "" {
			return
		}
		urls = append(urls, value)
	}

	appendURL(primary)

	// For well-known papers, keep a curated implementation near the front.
	// This gives paper reproduction a trustworthy fallback when GitHub Search
	// returns a stale or oversized repository.
	if task != nil {
		for _, candidate := range curatedRepoFallbackCandidates(task.Description) {
			for _, repoURL := range candidate.RepoURLs {
				appendURL(repoURL)
			}
		}
	}

	if raw := strings.TrimSpace(taskInputValue(task, "candidate_repositories")); raw != "" {
		var candidates []repoCandidate
		if err := json.Unmarshal([]byte(raw), &candidates); err == nil {
			for _, candidate := range candidates {
				for _, repoURL := range candidate.RepoURLs {
					appendURL(repoURL)
				}
			}
		}
	}

	return uniqueNonEmptyStrings(urls)
}

func normalizeGitHubRepoURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimSuffix(value, ".git")
	value = strings.TrimRight(value, "/")
	return value
}

func cloneFirstAvailableRepository(ctx context.Context, candidateURLs []string) (string, string, []string, error) {
	candidateURLs = uniqueNonEmptyStrings(candidateURLs)
	if len(candidateURLs) == 0 {
		return "", "", nil, fmt.Errorf("repo_prepare: missing clone candidates")
	}
	if len(candidateURLs) > 5 {
		candidateURLs = candidateURLs[:5]
	}

	attempts := make([]string, 0, len(candidateURLs)*2)
	for _, repoURL := range candidateURLs {
		workspacePath, err := os.MkdirTemp("", "scholar_repo_workspace_")
		if err != nil {
			return "", "", attempts, fmt.Errorf("create repo workspace: %w", err)
		}

		if err := cloneRepositoryWithRetry(ctx, repoURL, workspacePath, &attempts); err != nil {
			_ = os.RemoveAll(workspacePath)
			continue
		}
		return repoURL, workspacePath, attempts, nil
	}

	return "", "", attempts, fmt.Errorf("clone repo failed after %d candidate(s): %s", len(candidateURLs), strings.Join(attempts, " | "))
}

func cloneRepositoryWithRetry(ctx context.Context, repoURL, workspacePath string, attempts *[]string) error {
	cloneCommands := [][]string{
		{"clone", "--depth", "1", "--filter=blob:none", "--single-branch", repoURL, workspacePath},
		{"clone", "--depth", "1", repoURL, workspacePath},
	}

	var lastErr error
	for idx, args := range cloneCommands {
		if idx > 0 {
			_ = os.RemoveAll(workspacePath)
			if err := os.MkdirAll(workspacePath, 0o755); err != nil {
				return fmt.Errorf("recreate repo workspace: %w", err)
			}
		}

		cloneCtx, cancel := context.WithTimeout(ctx, envDuration("REPO_CLONE_TIMEOUT", 45*time.Second))
		cmd := exec.CommandContext(cloneCtx, "git", args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if cmd.Process == nil {
				return nil
			}
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		cmd.WaitDelay = 5 * time.Second
		output, err := cmd.CombinedOutput()
		cancel()
		if err == nil {
			*attempts = append(*attempts, fmt.Sprintf("%s: ok", repoURL))
			return nil
		}

		msg := strings.TrimSpace(string(output))
		if msg == "" {
			msg = err.Error()
		} else {
			msg = fmt.Sprintf("%v (%s)", err, msg)
		}
		*attempts = append(*attempts, fmt.Sprintf("%s: %s", repoURL, msg))
		lastErr = fmt.Errorf("%s", msg)
	}
	return lastErr
}

func scanRepositoryWorkspace(workspacePath string) ([]string, []string, error) {
	dependencyFiles := make([]string, 0, 8)
	codeCandidates := make([]string, 0, 16)

	err := filepath.WalkDir(workspacePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			switch name {
			case ".git", ".github", "__pycache__", "node_modules", ".venv", "venv", "dist", "build", "docs":
				return filepath.SkipDir
			}
			return nil
		}

		lowerName := strings.ToLower(name)
		switch lowerName {
		case "requirements.txt", "environment.yml", "environment.yaml", "pyproject.toml", "setup.py", "setup.cfg", "pipfile":
			dependencyFiles = append(dependencyFiles, path)
		}

		if strings.HasSuffix(lowerName, ".py") {
			codeCandidates = append(codeCandidates, path)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("scan repository workspace: %w", err)
	}

	sort.SliceStable(codeCandidates, func(i, j int) bool {
		return codeFileScore(codeCandidates[i]) > codeFileScore(codeCandidates[j])
	})
	return dependencyFiles, codeCandidates, nil
}

func choosePreferredCodeFile(candidates []string) string {
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return ""
}

func codeFileScore(path string) int {
	score := 0
	base := strings.ToLower(filepath.Base(path))
	full := strings.ToLower(path)

	switch base {
	case "the_annotated_transformer.py":
		score += 100
	case "main.py":
		score += 50
	case "train.py":
		score += 40
	case "run.py":
		score += 30
	}

	if strings.Contains(full, "annotated") && strings.Contains(full, "transformer") {
		score += 60
	}
	if strings.Contains(full, "attention") && strings.Contains(full, "transformer") {
		score += 30
	}
	if strings.Contains(full, "test") || strings.Contains(full, "example") || strings.Contains(full, "demo") {
		score -= 20
	}
	if strings.Contains(full, "tutorial") || strings.Contains(full, "notebook") {
		score -= 10
	}
	return score
}

func toWorkspaceRelativePaths(workspacePath string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		rel, err := filepath.Rel(workspacePath, value)
		if err != nil {
			out = append(out, value)
			continue
		}
		out = append(out, rel)
	}
	return out
}

func taskInputValue(task *models.Task, key string) string {
	if task == nil || task.Inputs == nil {
		return ""
	}
	value, ok := task.Inputs[key]
	if !ok || value == nil {
		return ""
	}
	return fmt.Sprint(value)
}
