package updater

import (
	"context"
	"os"
	"time"

	"github.com/blang/semver"
	"github.com/rhysd/go-github-selfupdate/selfupdate"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
	"naiveswitcher/service"
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

			// 原子性地停止旧进程、更新二进制文件并启动新进程
			state.NaiveCmdLock.Lock()
			defer state.NaiveCmdLock.Unlock()

			// 1. 停止当前进程
			if state.NaiveCmd != nil {
				if err := state.NaiveCmd.Process.Kill(); err != nil {
					service.DebugF("Error killing naive: %v\n", err)
				}
				state.NaiveCmd.Wait()
				state.NaiveCmd = nil
			}

			// 2. 更新二进制文件
			os.Remove(service.BasePath + "/" + service.Naive)
			service.Naive = newNaive

			// 3. 启动新进程（检查是否正在关闭）
			select {
			case <-state.AppContext.Done():
				service.DebugF("Application is shutting down, skipping naive restart after update\n")
				return
			default:
			}

			state.NaiveCmd, err = service.NaiveCmd(state.FastestUrl)
			if err != nil {
				service.DebugF("Error creating naive command after update: %v\n", err)
				return
			}
			if err := state.NaiveCmd.Start(); err != nil {
				service.DebugF("Error starting naive after update: %v\n", err)
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
