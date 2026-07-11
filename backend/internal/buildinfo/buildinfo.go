package buildinfo

import "runtime"

var (
	Version   = "s4-runtime"
	Commit    = "unknown"
	BuildDate = "development"
)

type InfoPayload struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuiltAt   string `json:"built_at"`
	GoVersion string `json:"go_version"`
	GoOS      string `json:"goos"`
	GoArch    string `json:"goarch"`
}

func Info() InfoPayload {
	return InfoPayload{
		Name:      "steward",
		Version:   Version,
		Commit:    Commit,
		BuiltAt:   BuildDate,
		GoVersion: runtime.Version(),
		GoOS:      runtime.GOOS,
		GoArch:    runtime.GOARCH,
	}
}
