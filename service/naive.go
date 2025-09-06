package service

import (
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/semver"

	"naiveswitcher/internal/types"
)

// naive version: naiveproxy-v130.0.6723.40-5-mac-x64
func NaiveCmd(state *types.GlobalState, proxy string) (*exec.Cmd, error) {
	if Naive == "" {
		return nil, errors.New("no naive found")
	}
	if proxy == "" {
		return nil, errors.New("no proxy found")
	}
	return exec.CommandContext(state.AppContext, BasePath+"/"+Naive, "--listen=socks://"+UpstreamListenPort, "--proxy="+proxy), nil
}

func getNaiveList() []string {
	var naiveList []string
	filepath.Walk(BasePath, func(p string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}
		fileName := path.Base(p)
		if !strings.HasPrefix(fileName, "naiveproxy") {
			return nil
		}
		naiveList = append(naiveList, fileName)
		return nil
	})
	return naiveList
}

func getNaiveVersion(fileName string) string {
	split := strings.Split(fileName, "-")
	if len(split) > 1 && semver.IsValid(split[1]) {
		return split[1]
	}
	return "v0.0.0"
}

func getNaiveOsArchSuffix(fileName string) (string, error) {
	split := strings.Split(fileName, "-")
	if len(split) > 3 {
		return strings.Join(split[3:], "-"), nil
	}
	return "", errors.New("no os arch suffix found")
}

func getLatestLocalNaiveVersion(naiveList []string) (string, error) {
	if len(naiveList) == 0 {
		return "", errors.New("no naive found")
	}
	slices.SortFunc(naiveList, func(a, b string) int {
		return semver.Compare(getNaiveVersion(a), getNaiveVersion(b))
	})
	return naiveList[len(naiveList)-1], nil
}

func assetUrlToBinaryName(url string) string {
	fileName := path.Base(url)
	if strings.HasSuffix(fileName, ".tar.xz") {
		fileName = strings.TrimSuffix(fileName, ".tar.xz")
	} else {
		fileName = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	}
	return fileName
}
