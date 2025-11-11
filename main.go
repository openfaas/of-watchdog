// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/openfaas/of-watchdog/config"
	"github.com/openfaas/of-watchdog/pkg"
)

func main() {
	var runHealthcheck bool
	var versionFlag bool

	flag.BoolVar(&versionFlag, "version", false, "Print the version and exit")
	flag.BoolVar(&runHealthcheck,
		"run-healthcheck",
		false,
		"Check for the a lock-file, when using an exec healthcheck. Exit 0 for present, non-zero when not found.")

	flag.Parse()

	printVersion()

	if versionFlag {
		return
	}

	watchdogConfig, err := config.New(os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %s", err.Error())
		os.Exit(1)
	}

	w := pkg.NewWatchdog(watchdogConfig)

	if runHealthcheck {
		if w.LockFilePresent() {
			os.Exit(0)
		}

		fmt.Fprintf(os.Stderr, "unable to find lock file.\n")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := w.Start(ctx); err != nil {
		log.Printf("Error: %s\n", err.Error())
		os.Exit(1)
	}
}

func printVersion() {
	sha := "unknown"
	if len(GitCommit) > 0 {
		sha = GitCommit
	}

	log.Printf("Version: %v\tSHA: %v\n", BuildVersion(), sha)
}
