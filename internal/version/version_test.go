package version

import (
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetPrefersExplicitReleaseMetadata(t *testing.T) {
	restoreGlobals := setVersionGlobals("v1.2.3", "abc123", "2026-06-26T00:00:00Z")
	defer restoreGlobals()
	restoreBuildInfo := setBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.2"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "older"},
			{Key: "vcs.time", Value: "2026-06-25T00:00:00Z"},
		},
	}, true)
	defer restoreBuildInfo()

	info := Get()

	assert.Equal(t, "v1.2.3", info.Version)
	assert.Equal(t, "abc123", info.Commit)
	assert.Equal(t, "2026-06-26T00:00:00Z", info.Date)
	assert.NotEmpty(t, info.GoVersion)
	assert.NotEmpty(t, info.Platform)
}

func TestGetUsesGoBuildInfoForDefaultMetadata(t *testing.T) {
	restoreGlobals := setVersionGlobals(defaultVersion, defaultCommit, defaultDate)
	defer restoreGlobals()
	restoreBuildInfo := setBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Version: "v1.2.3"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.time", Value: "2026-06-26T00:00:00Z"},
		},
	}, true)
	defer restoreBuildInfo()

	info := Get()

	assert.Equal(t, "v1.2.3", info.Version)
	assert.Equal(t, "abc123", info.Commit)
	assert.Equal(t, "2026-06-26T00:00:00Z", info.Date)
}

func TestGetFallsBackWhenGoBuildInfoIsUnavailable(t *testing.T) {
	restoreGlobals := setVersionGlobals(defaultVersion, defaultCommit, defaultDate)
	defer restoreGlobals()
	restoreBuildInfo := setBuildInfo(nil, false)
	defer restoreBuildInfo()

	info := Get()

	assert.Equal(t, defaultVersion, info.Version)
	assert.Equal(t, defaultCommit, info.Commit)
	assert.Equal(t, defaultDate, info.Date)
}

func TestSelectBuildValuePrefersExplicitValue(t *testing.T) {
	assert.Equal(t, "v1.2.3", selectBuildValue("v1.2.3", defaultVersion, "v1.2.2"))
}

func TestSelectBuildValueUsesBuildInfoForDefaultValue(t *testing.T) {
	assert.Equal(t, "v1.2.3", selectBuildValue(defaultVersion, defaultVersion, "v1.2.3"))
}

func TestSelectBuildValueFallsBackToDefault(t *testing.T) {
	assert.Equal(t, defaultVersion, selectBuildValue("", defaultVersion, ""))
}

func TestNormalizeBuildInfoValue(t *testing.T) {
	assert.Empty(t, normalizeBuildInfoValue(""))
	assert.Empty(t, normalizeBuildInfoValue("(devel)"))
	assert.Equal(t, "v1.2.3", normalizeBuildInfoValue("v1.2.3"))
}

func setVersionGlobals(version, commit, date string) func() {
	oldVersion := Version
	oldCommit := Commit
	oldDate := Date

	Version = version
	Commit = commit
	Date = date

	return func() {
		Version = oldVersion
		Commit = oldCommit
		Date = oldDate
	}
}

func setBuildInfo(info *debug.BuildInfo, ok bool) func() {
	oldReadBuildInfo := readBuildInfo
	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return info, ok
	}
	return func() {
		readBuildInfo = oldReadBuildInfo
	}
}
