//go:build windows

package main

import "errors"

func execSelf() error { return errors.New("restarting itself is not supported on Windows") }

const canRestart = false
