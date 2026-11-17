//go:build !windows

package main

import "fmt"

const defaultTrayPaused = false

func cmdPreview([]string) { die(fmt.Errorf("native preview is available on Windows")) }
