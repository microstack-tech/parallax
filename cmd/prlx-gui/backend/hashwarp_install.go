// Copyright 2026 The Parallax Authors
// This file is part of the Parallax library.

package backend

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/ParallaxProtocol/parallax/log"
	"github.com/ulikunitz/xz"
)

const (
	hashwarpReleasesAPI = "https://api.github.com/repos/ParallaxProtocol/hashwarp/releases/latest"
)

// InstallHashwarp downloads and installs the hashwarp binary for the given
// GPU type ("cuda" or "opencl"). It fetches the latest release from GitHub,
// finds the matching asset for the current OS, downloads and extracts it,
// and places the hashwarp binary next to the prlx-gui executable.
//
// The emit function is called with progress updates:
//   - {"step": "downloading", "pct": 0..100}
//   - {"step": "extracting"}
//   - {"step": "done"}
//   - {"step": "error", "msg": "..."}
func (m *MinerController) InstallHashwarp(gpuType string, emit func(step string, detail string)) error {
	if gpuType != "cuda" && gpuType != "opencl" {
		return fmt.Errorf("gpu type must be 'cuda' or 'opencl', got %q", gpuType)
	}

	goos := runtime.GOOS
	if goos != "linux" && goos != "windows" {
		return fmt.Errorf("unsupported OS %q for hashwarp install", goos)
	}

	emit("finding", "Looking up latest release...")
	log.Info("Installing hashwarp", "gpu", gpuType, "os", goos)

	// 1. Fetch latest release metadata from GitHub.
	assetURL, assetName, err := findHashwarpAsset(gpuType, goos)
	if err != nil {
		return fmt.Errorf("find release: %w", err)
	}
	log.Info("Found hashwarp asset", "name", assetName, "url", assetURL)

	// 2. Download the asset.
	emit("downloading", assetName)
	tmpFile, err := downloadFile(assetURL)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer os.Remove(tmpFile)

	// 3. Determine where to place the binary.
	installDir, err := hashwarpInstallDir()
	if err != nil {
		return fmt.Errorf("install dir: %w", err)
	}

	// 4. Extract the hashwarp binary from the archive.
	emit("extracting", "")
	binaryName := "hashwarp"
	if goos == "windows" {
		binaryName = "hashwarp.exe"
	}

	var extractErr error
	if strings.HasSuffix(assetName, ".zip") {
		extractErr = extractFromZip(tmpFile, binaryName, installDir)
	} else if strings.HasSuffix(assetName, ".tar.xz") {
		extractErr = extractFromTarXz(tmpFile, binaryName, installDir)
	} else if strings.HasSuffix(assetName, ".tar.gz") {
		extractErr = extractFromTarGz(tmpFile, binaryName, installDir)
	} else {
		extractErr = fmt.Errorf("unsupported archive format: %s", assetName)
	}
	if extractErr != nil {
		return fmt.Errorf("extract: %w", extractErr)
	}

	// 5. Ensure the binary is executable (Linux).
	destPath := filepath.Join(installDir, binaryName)
	if goos != "windows" {
		if err := os.Chmod(destPath, 0o755); err != nil {
			return fmt.Errorf("chmod: %w", err)
		}
	}

	emit("done", destPath)
	log.Info("Hashwarp installed", "path", destPath)
	return nil
}

// findHashwarpAsset queries the GitHub releases API and returns the download
// URL and filename for the matching asset.
func findHashwarpAsset(gpuType, goos string) (url, name string, err error) {
	resp, err := http.Get(hashwarpReleasesAPI)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", "", fmt.Errorf("GitHub API returned %d", resp.StatusCode)
	}

	var release struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", fmt.Errorf("decode release: %w", err)
	}

	// Match pattern: hashwarp-*-{cuda12|opencl}-{linux|windows}.{ext}
	// GPU type in asset names uses "cuda12" for CUDA.
	gpuToken := gpuType
	if gpuType == "cuda" {
		gpuToken = "cuda" // matches "cuda12" via Contains
	}

	for _, a := range release.Assets {
		lower := strings.ToLower(a.Name)
		if strings.Contains(lower, gpuToken) && strings.Contains(lower, goos) {
			return a.BrowserDownloadURL, a.Name, nil
		}
	}

	return "", "", fmt.Errorf("no matching asset found for gpu=%s os=%s (checked %d assets)", gpuType, goos, len(release.Assets))
}

// downloadFile downloads a URL to a temporary file and returns its path.
func downloadFile(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download returned %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "hashwarp-download-*")
	if err != nil {
		return "", err
	}

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return "", err
	}
	tmp.Close()
	return tmp.Name(), nil
}

// hashwarpInstallDir returns the directory where hashwarp should be placed
// (same directory as the running prlx-gui binary).
func hashwarpInstallDir() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Dir(exe), nil
}

// extractFromZip finds and extracts a specific binary from a zip archive.
func extractFromZip(zipPath, binaryName, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		if filepath.Base(f.Name) == binaryName {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			defer rc.Close()

			destPath := filepath.Join(destDir, binaryName)
			out, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer out.Close()

			_, err = io.Copy(out, rc)
			return err
		}
	}
	return fmt.Errorf("%s not found in zip archive", binaryName)
}

// extractFromTarGz finds and extracts a specific binary from a tar.gz archive.
func extractFromTarGz(archivePath, binaryName, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()

	return extractFromTar(tar.NewReader(gz), binaryName, destDir)
}

// extractFromTarXz finds and extracts a specific binary from a tar.xz archive.
func extractFromTarXz(archivePath, binaryName, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	xzReader, err := xz.NewReader(f)
	if err != nil {
		return err
	}

	return extractFromTar(tar.NewReader(xzReader), binaryName, destDir)
}

// extractFromTar walks a tar reader looking for the named binary.
func extractFromTar(tr *tar.Reader, binaryName, destDir string) error {
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		if filepath.Base(hdr.Name) == binaryName && hdr.Typeflag == tar.TypeReg {
			destPath := filepath.Join(destDir, binaryName)
			out, err := os.Create(destPath)
			if err != nil {
				return err
			}
			defer out.Close()

			_, err = io.Copy(out, tr)
			return err
		}
	}
	return fmt.Errorf("%s not found in tar archive", binaryName)
}
