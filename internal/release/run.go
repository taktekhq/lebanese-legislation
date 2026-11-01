package release

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
)

// The release commands share these two helpers rather than internal/cli,
// which pulls in the whole database build and its embedded data: the db
// image builds restore-release and check-release from a slim context.

// Context is cancelled on Ctrl-C and SIGTERM.
func Context() (context.Context, context.CancelFunc) {
	log.SetFlags(0)
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// Exit ends the run with status 1 on err, after printing it. A nil err
// returns.
func Exit(err error) {
	if err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
