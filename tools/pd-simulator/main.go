// Copyright 2017 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BurntSushi/toml"
	"pd/pkg/logutil"
	"pd/server"
	"pd/server/api"
	"pd/server/schedule"
	"pd/tools/pd-simulator/simulator"
	"pd/tools/pd-simulator/simulator/cases"
	"pd/tools/pd-simulator/simulator/simutil"
	log "github.com/sirupsen/logrus"

	// Register schedulers.
	_ "pd/server/schedulers"
	// Register namespace classifiers.
	_ "pd/table"
)

var (
	pdAddr         = flag.String("pd", "", "pd address")
	configFile     = flag.String("config", "conf/simconfig.toml", "config file")
	caseName       = flag.String("case", "", "case name")
	serverLogLevel = flag.String("serverLog", "fatal", "pd server log level.")
	simLogLevel    = flag.String("simLog", "fatal", "simulator log level.")
)

func main() {
	flag.Parse()

	simutil.InitLogger(*simLogLevel)
	schedule.Simulating = true

	if *caseName == "" {
		if *pdAddr != "" {
			simutil.Logger.Fatal("need to specify one config name")
		}
		for simCase := range cases.CaseMap {
			run(simCase)
		}
	} else {
		run(*caseName)
	}
}

func run(simCase string) {
	simConfig := simulator.NewSimConfig(*serverLogLevel)
	if *configFile != "" {
		if _, err := toml.DecodeFile(*configFile, simConfig); err != nil {
			simutil.Logger.Fatal(err)
		}
	}
	simConfig.Adjust()

	if *pdAddr != "" {
		simStart(*pdAddr, simCase, simConfig)
	} else {
		local, clean := NewSingleServer(simConfig)
		err := local.Run(context.Background())
		if err != nil {
			simutil.Logger.Fatal("run server error:", err)
		}
		for {
			if local.IsLeader() {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		simStart(local.GetAddr(), simCase, simConfig, clean)
	}
}

// NewSingleServer creates a pd server for simulator.
func NewSingleServer(simConfig *simulator.SimConfig) (*server.Server, server.CleanupFunc) {
	err := logutil.InitLogger(&simConfig.ServerConfig.Log)
	if err != nil {
		log.Fatalf("initialize logger error: %s\n", err)
	}

	s, err := server.CreateServer(simConfig.ServerConfig, api.NewHandler)
	if err != nil {
		panic("create server failed")
	}

	cleanup := func() {
		s.Close()
		cleanServer(simConfig.ServerConfig)
	}
	return s, cleanup
}

func cleanServer(cfg *server.Config) {
	// Clean data directory
	os.RemoveAll(cfg.DataDir)
}

func simStart(pdAddr string, simCase string, simConfig *simulator.SimConfig, clean ...server.CleanupFunc) {
	start := time.Now()
	driver, err := simulator.NewDriver(pdAddr, simCase, simConfig)
	if err != nil {
		simutil.Logger.Fatal("create driver error:", err)
	}

	err = driver.Prepare()
	if err != nil {
		simutil.Logger.Fatal("simulator prepare error:", err)
	}

	tickInterval := simConfig.SimTickInterval.Duration

	tick := time.NewTicker(tickInterval)
	defer tick.Stop()
	sc := make(chan os.Signal, 1)
	signal.Notify(sc,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT)

	simResult := "FAIL"

EXIT:
	for {
		select {
		case <-tick.C:
			driver.Tick()
			if driver.Check() {
				simResult = "OK"
				break EXIT
			}
		case <-sc:
			break EXIT
		}
	}

	driver.Stop()
	if len(clean) != 0 {
		clean[0]()
	}

	fmt.Printf("%s [%s] total iteration: %d, time cost: %v\n", simResult, simCase, driver.TickCount(), time.Since(start))
	driver.PrintStatistics()

	if simResult != "OK" {
		os.Exit(1)
	}
}
