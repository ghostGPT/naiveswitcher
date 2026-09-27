package common

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
)

var (
	Debug bool

	BasePath     string
	naiveVersion string
	naiveMutex   sync.RWMutex
)

func GetNaive() string {
	naiveMutex.RLock()
	defer naiveMutex.RUnlock()
	return naiveVersion
}

func SetNaive(version string) {
	naiveMutex.Lock()
	naiveVersion = version
	naiveMutex.Unlock()
}

const (
	UpstreamListenPort = "127.0.0.1:10790"
)

func Init() {
	ex, err := os.Executable()
	if err != nil {
		panic(err)
	}
	BasePath = filepath.Dir(ex)
	f, err := os.Create(BasePath + "/crash.txt")
	if err != nil {
		panic(err) // note there is a bit of a catch-22 here
	}
	if err := debug.SetCrashOutput(f, debug.CrashOptions{}); err != nil {
		panic(err)
	}
}
