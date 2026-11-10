package hosttools

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func downloadAndInstallZip(ctx context.Context, client *http.Client, j *job, toolID, assetURL string) (int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	resp, err := client.Do(req)
	if err != nil {
		return 1, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1, fmt.Errorf("下载失败: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256<<20))
	if err != nil {
		return 1, err
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 1, err
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || filepath.Base(f.Name) != toolID || f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if f.UncompressedSize64 > 256<<20 {
			return 1, fmt.Errorf("二进制过大")
		}
		rc, err := f.Open()
		if err != nil {
			return 1, err
		}
		blob, err := io.ReadAll(io.LimitReader(rc, 256<<20))
		rc.Close()
		if err != nil {
			return 1, err
		}
		dir := "/usr/local/bin"
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".local", "bin")
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return 1, err
		}
		dst := filepath.Join(dir, toolID)
		tmp := dst + ".ghoul-tmp"
		if err := os.WriteFile(tmp, blob, 0o755); err != nil {
			return 1, err
		}
		if err := os.Rename(tmp, dst); err != nil {
			_ = os.Remove(tmp)
			return 1, err
		}
		j.append("[ok] " + toolID + " -> " + dst + "\n")
		if !strings.Contains(os.Getenv("PATH"), dir) {
			j.append("[warn] " + dir + " 不在当前服务 PATH，请将其加入 PATH\n")
		}
		return 0, nil
	}
	return 1, fmt.Errorf("压缩包内未找到 %s", toolID)
}
