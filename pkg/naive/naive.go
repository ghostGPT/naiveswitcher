package naive

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/semver"

	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/common"
	"naiveswitcher/pkg/log"
)

// Init 初始化 naive，查找最新的本地版本
func Init() error {
	var err error
	common.Naive, err = getLatestLocalNaiveVersion(getNaiveList())
	return err
}

// naive version: naiveproxy-v130.0.6723.40-5-mac-x64
func NaiveCmd(state *types.GlobalState, proxy string) (*exec.Cmd, context.CancelFunc, error) {
	if common.Naive == "" {
		return nil, nil, errors.New("no naive found")
	}
	if proxy == "" {
		return nil, nil, errors.New("no proxy found")
	}
	// 创建一个可取消的子context
	ctx, cancel := context.WithCancel(state.AppContext)
	cmd := exec.CommandContext(ctx, common.BasePath+"/"+common.Naive, "--listen=socks://"+common.UpstreamListenPort, "--proxy="+proxy)

	// 设置进程组，确保可以杀死整个进程树
	cmd.SysProcAttr = getSysProcAttr()

	return cmd, cancel, nil
}

func getNaiveList() []string {
	var naiveList []string
	err := filepath.Walk(common.BasePath, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
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
	if err != nil {
		log.DebugF("Error walking filepath: %v\n", err)
	}
	return naiveList
}

func getNaiveVersion(fileName string) string {
	split := strings.Split(fileName, "-")
	if len(split) > 1 && semver.IsValid(split[1]) {
		return split[1]
	}
	return "v0.0.0"
}

func GetNaiveOsArchSuffix(fileName string) (string, error) {
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

func AssetUrlToBinaryName(url string) string {
	fileName := path.Base(url)
	if strings.HasSuffix(fileName, ".tar.xz") {
		fileName = strings.TrimSuffix(fileName, ".tar.xz")
	} else {
		fileName = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	}
	return fileName
}
