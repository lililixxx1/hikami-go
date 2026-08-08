// +build ignore

// upload-release.go — 创建 GitHub Release（如不存在）并上传 dist/ 下所有产物。
// 用法: go run scripts/upload-release.go -repo lililixxx1/hikami-go -tag v0.1.0 -token <PAT>
//
// token 来源优先级: -token 参数 > HIKAMI_RELEASE_TOKEN 环境变量 > git credential fill(github.com)。
// 覆盖语义: 若同名 asset 已存在则先删除再上传（幂等）。
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	repo := flag.String("repo", "", "owner/repo, e.g. lililixxx1/hikami-go")
	tag := flag.String("tag", "", "tag name, e.g. v0.1.0")
	title := flag.String("title", "", "release title (defaults to tag)")
	notes := flag.String("notes", "", "release body markdown")
	distDir := flag.String("dist", "dist", "directory of artifacts to upload")
	tokenFlag := flag.String("token", "", "GitHub token (PAT or OAuth); if empty, falls back to env/git credential")
	draft := flag.Bool("draft", false, "create as draft")
	prerelease := flag.Bool("prerelease", false, "mark as prerelease")
	flag.Parse()

	if *repo == "" || *tag == "" {
		fmt.Fprintln(os.Stderr, "ERROR: -repo and -tag are required")
		os.Exit(2)
	}

	token := *tokenFlag
	if token == "" {
		token = os.Getenv("HIKAMI_RELEASE_TOKEN")
	}
	if token == "" {
		t, err := tokenFromGitCredential()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: no token via -token/env/git-credential: %v\n", err)
			os.Exit(2)
		}
		token = t
		fmt.Println("INFO: using token from git credential store")
	}
	fmt.Printf("INFO: token prefix=%s len=%d\n", safePrefix(token), len(token))

	// 1. 校验 token + 身份
	var who struct{ Login string `json:"login"` }
	if err := apiJSON(token, "GET", "https://api.github.com/user", nil, &who); err != nil {
		die("token check failed (/user): %v", err)
	}
	fmt.Printf("INFO: authenticated as %s\n", who.Login)

	// 2. 获取或创建 Release
	rel, err := getOrCreateRelease(token, *repo, *tag, *title, *notes, *draft, *prerelease)
	if err != nil {
		die("get/create release: %v", err)
	}
	fmt.Printf("INFO: release id=%d name=%q draft=%v prerelease=%v\n", rel.ID, rel.Name, rel.Draft, rel.Prerelease)
	fmt.Printf("INFO: html_url=%s\n", rel.HTMLURL)
	fmt.Printf("INFO: upload_url_template=%s\n", rel.UploadURL)

	// 3. 收集要上传的产物
	files, err := listArtifacts(*distDir)
	if err != nil {
		die("list artifacts: %v", err)
	}
	fmt.Printf("INFO: %d artifact(s) to upload from %s\n", len(files), *distDir)

	// 4. 逐个上传（幂等：删旧→传新）
	for _, f := range files {
		if err := uploadArtifact(token, rel, f); err != nil {
			die("upload %s: %v", filepath.Base(f), err)
		}
		fmt.Printf("  [OK] %s\n", filepath.Base(f))
	}

	fmt.Println("\nDONE. Release URL:", rel.HTMLURL)
}

type release struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	TagName    string `json:"tag_name"`
	Body       string `json:"body"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	HTMLURL    string `json:"html_url"`
	UploadURL  string `json:"upload_url"`
	Assets     []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"assets"`
}

func getOrCreateRelease(token, repo, tag, title, notes string, draft, prerelease bool) (*release, error) {
	// 先查 tag 是否已有 release
	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/tags/%s", repo, tag)
	var existing release
	err := apiJSON(token, "GET", url, nil, &existing)
	if err == nil && existing.ID != 0 {
		fmt.Printf("INFO: release already exists (id=%d), will (re-)upload assets\n", existing.ID)
		return &existing, nil
	}
	if !strings.Contains(err.Error(), "404") {
		return nil, fmt.Errorf("lookup release: %w", err)
	}
	// 不存在 → 创建
	if title == "" {
		title = tag
	}
	body := map[string]any{
		"tag_name":   tag,
		"name":       title,
		"body":       notes,
		"draft":      draft,
		"prerelease": prerelease,
	}
	payload, _ := json.Marshal(body)
	var created release
	if err := apiJSON(token, "POST", fmt.Sprintf("https://api.github.com/repos/%s/releases", repo), payload, &created); err != nil {
		return nil, fmt.Errorf("create release: %w", err)
	}
	return &created, nil
}

func uploadArtifact(token string, rel *release, path string) error {
	name := filepath.Base(path)

	// 删除已存在的同名 asset（幂等）
	for _, a := range rel.Assets {
		if a.Name == name {
			delURL := fmt.Sprintf("https://api.github.com/repos/lililixxx1/hikami-go/releases/assets/%d", a.ID)
			req, _ := http.NewRequest("DELETE", delURL, nil)
			req.Header.Set("Authorization", "token "+token)
			req.Header.Set("Accept", "application/vnd.github+json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("delete old asset: %w", err)
			}
			resp.Body.Close()
			fmt.Printf("  (removed existing asset id=%d name=%s)\n", a.ID, name)
		}
	}

	// upload_url 形如 "https://uploads.github.com/.../assets{?name,label}"
	uploadURL := strings.SplitN(rel.UploadURL, "{", 2)[0]
	uploadURL += "?name=" + name

	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", uploadURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = int64(len(data))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("upload failed: HTTP %d: %s", resp.StatusCode, string(rb))
	}
	return nil
}

func listArtifacts(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if strings.HasPrefix(n, ".") {
			continue
		}
		out = append(out, filepath.Join(dir, n))
	}
	return out, nil
}

func apiJSON(token, method, url string, body []byte, out any) error {
	var br io.Reader
	if body != nil {
		br = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, url, br)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == 404 {
		return fmt.Errorf("404 not found")
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(rb))
	}
	if out != nil {
		if err := json.Unmarshal(rb, out); err != nil {
			return fmt.Errorf("decode: %w (body: %s)", err, string(rb))
		}
	}
	return nil
}

func tokenFromGitCredential() (string, error) {
	// 通过 git credential fill 读取 github.com 的凭据
	in := "protocol=https\nhost=github.com\n\n"
	cmd := exec.Command("git", "credential", "fill")
	cmd.Stdin = strings.NewReader(in)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "password=") {
			return strings.TrimPrefix(line, "password="), nil
		}
	}
	return "", fmt.Errorf("no password in git credential output")
}

func safePrefix(t string) string {
	if len(t) < 6 {
		return "***"
	}
	return t[:6]
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format+"\n", args...)
	os.Exit(1)
}
