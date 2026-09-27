package github

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-github/v68/github"
	"github.com/ulikunitz/xz"

	"naiveswitcher/pkg/common"
	"naiveswitcher/pkg/naive"
)

func GitHubCheckGetLatestRelease(ctx context.Context, owner string, repo string, currentVersion string) (*string, error) {
	client := github.NewClient(nil)
	releases, _, err := client.Repositories.GetLatestRelease(ctx, owner, repo)
	if err != nil {
		return nil, err
	}
	if strings.Contains(currentVersion, *releases.TagName) {
		return nil, nil
	}
	currentVersionSuffix, err := naive.GetNaiveOsArchSuffix(currentVersion)
	if err != nil {
		return nil, err
	}
	for _, asset := range releases.Assets {
		if strings.HasPrefix(*asset.Name, "naiveproxy") && strings.Contains(*asset.Name, currentVersionSuffix) {
			return asset.BrowserDownloadURL, nil
		}
	}
	return nil, errors.New("no asset found")
}

func GitHubDownloadAsset(ctx context.Context, url string) (string, error) {
	binaryName := naive.AssetUrlToBinaryName(url)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req = req.WithContext(ctx)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	uncompressedStream, err := xz.NewReader(resp.Body)
	if err != nil {
		return "", err
	}
	tarReader := tar.NewReader(uncompressedStream)

	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch header.Typeflag {
		case tar.TypeReg:
			if filepath.Base(header.Name) != "naive" {
				continue
			}
			outFile, err := os.CreateTemp(common.BasePath, binaryName+"-*.tmp")
			if err != nil {
				return "", err
			}
			defer os.Remove(outFile.Name())
			defer outFile.Close()
			if err := outFile.Chmod(0o755); err != nil {
				return "", err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				return "", err
			}
			if err := outFile.Close(); err != nil {
				return "", err
			}
			target := filepath.Join(common.BasePath, binaryName)
			if err := os.Rename(outFile.Name(), target); err != nil {
				if !os.IsExist(err) {
					return "", err
				}
				if err := os.Remove(target); err != nil {
					return "", err
				}
				if err := os.Rename(outFile.Name(), target); err != nil {
					return "", err
				}
			}
			return binaryName, nil
		}
	}

	return "", errors.New("no naive found")
}
