package updater

import (
	"context"
	"os"
	"time"

	"github.com/blang/semver"
	"github.com/rhysd/go-github-selfupdate/selfupdate"

	"naiveswitcher/internal/config"
	"naiveswitcher/service"
	"naiveswitcher/switcher"
	"naiveswitcher/internal/types"
)

// Updater 处理更新检查
func Updater(state *types.GlobalState, config *config.Config, gracefulShutdown context.CancelFunc, signal <-chan struct{}) {
	var checking bool
	for range signal {
		if checking {
			return
		}
		checking = true
		go func() {
			defer func() {
				checking = false
			}()

			service.DebugF("Checking for naive update\n")
			ctx, cancel := context.WithTimeout(context.Background(), (time.Duration(config.AutoSwitchDuration/2))*time.Minute)
			defer cancel()

			latestNaiveVersion, err := service.GitHubCheckGetLatestRelease(ctx, "klzgrad", "naiveproxy", service.Naive)
			if err != nil {
				service.DebugF("Error getting latest remote naive version: %v\n", err)
			}
			if latestNaiveVersion == nil {
				service.DebugF("No new version\n")
				return
			}

			newNaive, err := service.GitHubDownloadAsset(ctx, *latestNaiveVersion)
			if err != nil {
				service.DebugF("Error downloading asset: %v\n", err)
				return
			}

			// 停止当前进程并更新二进制文件
			state.NaiveCmdLock.Lock()
			if state.NaiveCmd != nil {
				if err := state.NaiveCmd.Process.Kill(); err != nil {
					service.DebugF("Error killing naive: %v\n", err)
				}
				state.NaiveCmd.Wait()
				state.NaiveCmd = nil
			}
			state.NaiveCmdLock.Unlock()

			os.Remove(service.BasePath + "/" + service.Naive)
			service.Naive = newNaive

			// 使用统一的重启函数
			if err := switcher.RestartNaive(state, state.FastestUrl); err != nil {
				service.DebugF("Error restarting naive after update: %v\n", err)
				return
			}

			service.DebugF("Updated to %s\n", service.Naive)
		}()

		go func() {
			v := semver.MustParse(config.Version)
			latest, err := selfupdate.UpdateSelf(v, "ghostGPT/naiveswitcher")
			if err != nil {
				service.DebugF("Binary update failed: %v", err)
				return
			}
			if latest.Version.LTE(v) {
				service.DebugF("Current binary is the latest version", config.Version)
			} else {
				service.DebugF("Successfully updated to version", latest.Version)
				service.DebugF("Release note:\n", latest.ReleaseNotes)
				gracefulShutdown()
			}
		}()
	}
}
