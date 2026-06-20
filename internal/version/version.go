package version

import "runtime"

var (
	// Version is overridden by release builds through -ldflags.
	Version = "dev"
	// Commit is the git commit used for this build.
	Commit = "none"
	// Date is the build timestamp, ideally in RFC3339/ISO-8601 format.
	Date = "unknown"
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

func Get() Info {
	return Info{
		Version:   valueOrDefault(Version, "dev"),
		Commit:    valueOrDefault(Commit, "none"),
		Date:      valueOrDefault(Date, "unknown"),
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

func valueOrDefault(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
