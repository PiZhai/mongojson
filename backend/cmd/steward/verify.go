package main

import "fmt"

func (c cli) verify(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printVerifyUsage()
		return nil
	}
	if len(args) == 0 {
		return c.verifyRuntime(args)
	}
	switch args[0] {
	case "runtime", "s3s4":
		return c.verifyRuntime(args[1:])
	case "service":
		return c.verifyService(args[1:])
	case "peers":
		return c.verifyPeers(args[1:])
	case "mesh":
		return c.verifyMesh(args[1:])
	case "evidence":
		return c.verifyEvidence(args[1:])
	default:
		return fmt.Errorf("unknown verify command %q", args[0])
	}
}
