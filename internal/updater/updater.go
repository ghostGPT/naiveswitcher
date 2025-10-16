package updater

import (
	"context"
	"os"
	"sync"
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
	var checkingLock sync.Mutex
	for range signal {
		checkingLock.Lock()
		if checking {
			checkingLock.Unlock()
			continue
		}
		checking = true
		checkingLock.Unlock()

		go func() {
			defer func() {
				checkingLock.Lock()
				checking = false
				checkingLock.Unlock()
			}()

			// 检查应用是否正在关闭
			select {
			case <-state.AppContext.Done():
				service.DebugF("Application is shutting down, skipping naive update check\n")
				return
			default:
			}

			service.DebugF("Checking for naive update\n")
			ctx, cancel := context.WithTimeout(context.Background(), (time.Duration(config.AutoSwitchDuration/2))*time.Minute)
			defer cancel()

			latestNaiveVersion, err := service.GitHubCheckGetLatestRelease(ctx, "klzgrad", "naiveproxy", service.Naive)
			if err != nil {
				service.DebugF("Error getting latest remote naive version: %v\n", err)
				return
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
				// 先取消 context
				if state.NaiveCmdCancel != nil {
					state.NaiveCmdCancel()
					state.NaiveCmdCancel = nil
				}
				// 强制杀死进程
				if state.NaiveCmd.Process != nil {
					if err := state.NaiveCmd.Process.Kill(); err != nil {
						service.DebugF("Error killing naive: %v\n", err)
					}
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

			state.NaiveCmd, state.NaiveCmdCancel, err = service.NaiveCmd(state, state.FastestUrl)
			if err != nil {
				service.DebugF("Error creating naive command after update: %v\n", err)
				return
			}
			if err := state.NaiveCmd.Start(); err != nil {
				service.DebugF("Error starting naive after update: %v\n", err)
				// 如果启动失败，取消 context 释放资源
				if state.NaiveCmdCancel != nil {
					state.NaiveCmdCancel()
					state.NaiveCmdCancel = nil
				}
				state.NaiveCmd = nil
				return
			}
			service.DebugF("Updated to %s (PID: %d)\n", service.Naive, state.NaiveCmd.Process.Pid)
		}()

		go func() {
			// 检查应用是否正在关闭
			select {
			case <-state.AppContext.Done():
				service.DebugF("Application is shutting down, skipping self-update check\n")
				return
			default:
			}

			v := semver.MustParse(config.Version)
			latest, err := selfupdate.UpdateSelf(v, config.UpdateRepo)
			if err != nil {
				service.DebugF("Binary update failed: %v\n", err)
				return
			}
			if latest.Version.LTE(v) {
				service.DebugF("Current binary is the latest version: %s\n", config.Version)
			} else {
				service.DebugF("Successfully updated to version: %s\n", latest.Version)
				service.DebugF("Release note:\n%s\n", latest.ReleaseNotes)
				gracefulShutdown()
			}
		}()
	}
}
