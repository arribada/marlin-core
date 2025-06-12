// Copyright (c) The Arribada initiative.
// Licensed under the GPLv3 License.

package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_addValuesFromEnv_UpdatesFromEnv(t *testing.T) {
	os.Setenv("I360_VIDEORRTSP_CAPTUREDEVICE", "haha")
	os.Setenv("I360_AUDIOR_ENABLED", "false")
	defer func() {
		os.Unsetenv("I360_VIDEORRTSP_CAPTUREDEVICE")
		os.Unsetenv("I360_AUDIOR_ENABLED")
	}()

	cfg := Config{}
	cfg.VideorRTSP.CaptureDevice = ""

	require.NoError(t, addValuesFromEnv(&cfg))
	assert.Equal(t, "haha", cfg.VideorRTSP.CaptureDevice)
}

func Test_addValuesFromEnv_KeepsDefaults(t *testing.T) {
	cfg := Config{}
	cfg.VideorRTSP.CaptureDevice = ""

	require.NoError(t, addValuesFromEnv(&cfg))
	assert.Equal(t, "", cfg.VideorRTSP.CaptureDevice)
}

func Test_addValuesFromEnv_ReturnsError(t *testing.T) {
	os.Setenv("I360_DATAWATCHER_UPLOADINTERVAL", "haha")
	defer func() {
		os.Unsetenv("I360_DATAWATCHER_UPLOADINTERVAL")
	}()
	cfg := Config{}
	cfg.DataWatcher.UploadInterval = 3600

	require.Error(t, addValuesFromEnv(&cfg))
}
