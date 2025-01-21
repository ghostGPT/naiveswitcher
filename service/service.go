package service

import (
	"os"
	"path/filepath"
	"runtime/debug"
)

var (
	Debug bool

	BasePath string
	Naive    string
)

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
	debug.SetCrashOutput(f, debug.CrashOptions{})
	Naive, err = getLatestLocalNaiveVersion(getNaiveList())
	if err != nil {
		panic(err)
	}
}
