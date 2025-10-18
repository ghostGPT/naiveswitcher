package updater

import (
	"context"
	"os"
	"sync/atomic"
	"time"

	"github.com/blang/semver"
	"github.com/rhysd/go-github-selfupdate/selfupdate"

	"naiveswitcher/internal/config"
	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/common"
	"naiveswitcher/pkg/github"
	"naiveswitcher/pkg/log"
	"naiveswitcher/pkg/naive"
)

// Updater 处理更新检查
// 注意：此函数在单个 goroutine 中运行，从 signal channel 顺序处理请求
// 使用原子标志避免并发检查，如果正在检查中则跳过新请求
func Updater(state *types.GlobalState, config *config.Config, gracefulShutdown context.CancelFunc, signal <-chan struct{}) {
	for range signal {
		// 检查是否正在更新，如果是则跳过
		if !atomic.CompareAndSwapInt32(&state.Checking, 0, 1) {
			log.DebugF("Already checking for updates, skipping request\n")
			continue
		}

		// 启动 naive 更新检查（异步，避免阻塞）
		go func() {
			defer atomic.StoreInt32(&state.Checking, 0) // 完成后重置标志

			// 检查应用是否正在关闭
			select {
			case <-state.AppContext.Done():
				log.DebugF("Application is shutting down, skipping naive update check\n")
				return
			default:
			}

			log.DebugF("Checking for naive update\n")
			ctx, cancel := context.WithTimeout(context.Background(), (time.Duration(config.AutoSwitchDuration/2))*time.Minute)
			defer cancel()

			latestNaiveVersion, err := github.GitHubCheckGetLatestRelease(ctx, "klzgrad", "naiveproxy", common.Naive)
			if err != nil {
				log.DebugF("Error getting latest remote naive version: %v\n", err)
				return
			}
			if latestNaiveVersion == nil {
				log.DebugF("No new version\n")
				return
			}

			newNaive, err := github.GitHubDownloadAsset(ctx, *latestNaiveVersion)
			if err != nil {
				log.DebugF("Error downloading asset: %v\n", err)
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
				// 使用 KillProcessGroup 终止进程
				if state.NaiveCmd.Process != nil {
					pid := state.NaiveCmd.Process.Pid
					naive.KillProcessGroup(state, pid)
				}
				state.NaiveCmd = nil
			}

			// 2. 更新二进制文件
			os.Remove(common.BasePath + "/" + common.Naive)
			common.Naive = newNaive

			// 3. 启动新进程（检查是否正在关闭）
			select {
			case <-state.AppContext.Done():
				log.DebugF("Application is shutting down, skipping naive restart after update\n")
				return
			default:
			}

			state.NaiveCmd, state.NaiveCmdCancel, err = naive.NaiveCmd(state, state.FastestUrl)
			if err != nil {
				log.DebugF("Error creating naive command after update: %v\n", err)
				return
			}
			if err := state.NaiveCmd.Start(); err != nil {
				log.DebugF("Error starting naive after update: %v\n", err)
				// 如果启动失败，取消 context 释放资源
				if state.NaiveCmdCancel != nil {
					state.NaiveCmdCancel()
					state.NaiveCmdCancel = nil
				}
				state.NaiveCmd = nil
				return
			}
			log.DebugF("Updated to %s (PID: %d)\n", common.Naive, state.NaiveCmd.Process.Pid)
		}()

		go func() {
			// 检查应用是否正在关闭
			select {
			case <-state.AppContext.Done():
				log.DebugF("Application is shutting down, skipping self-update check\n")
				return
			default:
			}

			log.DebugF("Checking for naiveswitcher self-update from repo: %s\n", config.UpdateRepo)
			v := semver.MustParse(config.Version)
			log.DebugF("Current naiveswitcher version: %s\n", config.Version)

			latest, err := selfupdate.UpdateSelf(v, config.UpdateRepo)
			if err != nil {
				log.DebugF("NaiveSwitcher update check failed: %v\n", err)
				return
			}

			// 检查返回值
			if latest == nil {
				log.DebugF("No naiveswitcher update information from GitHub\n")
				return
			}

			log.DebugF("NaiveSwitcher version comparison - Current: %s, GitHub latest: %s\n", v, latest.Version)

			if latest.Version.LTE(v) {
				log.DebugF("NaiveSwitcher is up to date (current: %s >= latest: %s)\n", config.Version, latest.Version)
			} else {
				log.DebugF("NaiveSwitcher updated from %s to %s\n", config.Version, latest.Version)
				log.DebugF("Release notes:\n%s\n", latest.ReleaseNotes)
				log.DebugF("Triggering graceful shutdown for restart...\n")
				gracefulShutdown()
			}
		}()
	}
}
