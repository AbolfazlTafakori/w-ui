// Command upgrade is the upgrade tests' tool, for the fixture generator and
// the CI jobs that install an old release and update it:
//
//	upgrade seed      put the fixture set into a running panel and write it down
//	upgrade verify    check a running panel against what was written down
//	upgrade snapshot  record what an install has on disk and in the kernel
//	upgrade compare   say what changed between two snapshots
//	upgrade fakegithub
//	                  answer as GitHub does, for an update from a test release
//	upgrade nodes     check a panel and a node of different releases work together
//	upgrade selfupdate
//	                  press the panel's update button and follow it until it is back
//
// It is not part of the panel and is never shipped; it lives in the module so
// it is built, vetted and tested with everything else.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "seed":
		err = seedCommand(os.Args[2:])
	case "verify":
		err = verifyCommand(os.Args[2:])
	case "snapshot":
		err = snapshotCommand(os.Args[2:])
	case "compare":
		err = compareCommand(os.Args[2:])
	case "fakegithub":
		err = fakeGitHubCommand(os.Args[2:])
	case "nodes":
		err = nodesCommand(os.Args[2:])
	case "selfupdate":
		err = selfUpdateCommand(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "upgrade:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: upgrade seed|verify|snapshot|compare|fakegithub|nodes|selfupdate [flags]")
}
