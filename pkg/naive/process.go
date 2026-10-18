package naive

import (
	"naiveswitcher/internal/types"
	"naiveswitcher/pkg/log"
)

// StartProcess requires NaiveCmdLock. Exactly one goroutine owns Cmd.Wait;
// shutdown, update and crash recovery all observe the same completion channel.
func StartProcess(state *types.GlobalState) error {
	state.AdvanceGeneration()
	state.NaiveDone = nil
	cmd := state.NaiveCmd
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	state.NaiveDone = done
	go func() {
		err := cmd.Wait()
		close(done)
		if err != nil {
			log.DebugF("Naive process exited: %v\n", err)
		}
	}()
	return nil
}
