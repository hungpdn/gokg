package version

import (
	"runtime"
	"runtime/debug"
)

const (
	defaultVersion = "dev"
	defaultCommit  = "none"
	defaultDate    = "unknown"
)

var (
	// Version is overridden by release builds through -ldflags.
	Version = defaultVersion
	// Commit is the git commit used for this build.
	Commit = defaultCommit
	// Date is the build timestamp, ideally in RFC3339/ISO-8601 format.
	Date = defaultDate

	readBuildInfo = debug.ReadBuildInfo
)

type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	Date      string `json:"date"`
	GoVersion string `json:"go_version"`
	Platform  string `json:"platform"`
}

func Get() Info {
	build := buildInfo()

	return Info{
		Version:   selectBuildValue(Version, defaultVersion, build.Version),
		Commit:    selectBuildValue(Commit, defaultCommit, build.Commit),
		Date:      selectBuildValue(Date, defaultDate, build.Date),
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
}

type metadata struct {
	Version string
	Commit  string
	Date    string
}

func buildInfo() metadata {
	info, ok := readBuildInfo()
	if !ok {
		return metadata{}
	}

	result := metadata{
		Version: normalizeBuildInfoValue(info.Main.Version),
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			result.Commit = normalizeBuildInfoValue(setting.Value)
		case "vcs.time":
			result.Date = normalizeBuildInfoValue(setting.Value)
		}
	}
	return result
}

func selectBuildValue(value, defaultValue, buildValue string) string {
	if value != "" && value != defaultValue {
		return value
	}
	if buildValue != "" {
		return buildValue
	}
	if value != "" {
		return value
	}
	return defaultValue
}

func normalizeBuildInfoValue(value string) string {
	if value == "" || value == "(devel)" {
		return ""
	}
	return value
}
