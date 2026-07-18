package privilegebroker

import ()

type processTreeGuard interface {
	Terminate() error
	Close() error
}

type brokerCommandResult struct {
	exitCode int
}

type capabilityTokenProfile string

const (
	capabilityTokenProfileProduction capabilityTokenProfile = "production"
	capabilityTokenProfileDefault    capabilityTokenProfile = "default"
	capabilityTokenProfileSystem     capabilityTokenProfile = "system-restricting-sid"
	capabilityTokenProfilePrivileges capabilityTokenProfile = "privileges-only"
)
