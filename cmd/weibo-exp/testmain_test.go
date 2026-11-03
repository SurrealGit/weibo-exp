package main

import (
	"os"
	"testing"
)

// TestMain points automatic session renewal at an unreachable address so no
// test in this package can reach the real Weibo passport.
func TestMain(m *testing.M) {
	if err := os.Setenv("WEIBO_EXP_PASSPORT_URL", "http://127.0.0.1:1"); err != nil {
		panic(err)
	}
	os.Exit(m.Run())
}
