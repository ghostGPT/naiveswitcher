package service

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-github/v68/github"
	"github.com/ulikunitz/xz"
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
	currentVersionSuffix, err := getNaiveOsArchSuffix(currentVersion)
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
	binaryName := assetUrlToBinaryName(url)

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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	uncompressedStream, err := xz.NewReader(bytes.NewReader(body))
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
			outFile, err := os.OpenFile(BasePath+"/"+binaryName, os.O_CREATE|os.O_WRONLY, 0755)
			defer outFile.Close()
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				return "", err
			}
			return binaryName, nil
		}
	}

	return "", errors.New("no naive found")
}
