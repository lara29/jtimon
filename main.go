package main

import (
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"sync"
	"syscall"

	flag "github.com/spf13/pflag"
)

const (
	// DefaultGRPCWindowSize is the default GRPC Window Size
	DefaultGRPCWindowSize = 1048576
	// MatchExpressionXpath is for the pattern matching the xpath and key-value pairs
	MatchExpressionXpath = "\\/([^\\/]*)\\[(.*?)+?(?:\\])"
	// MatchExpressionKey is for pattern matching the single and multiple key value pairs
	MatchExpressionKey = "([A-Za-z0-9-/]*)=(.*?)?(?:and|$)+"
)

var (
	cfgFile        = flag.StringSlice("config", make([]string, 0), "Config file name(s)")
	cfgFileList    = flag.String("config-file-list", "", "List of Config files")
	aliasFile      = flag.String("alias-file", "", "File containing aliasing information")
	expConfig      = flag.Bool("explore-config", false, "Explore full config of JTIMON and exit")
	print          = flag.Bool("print", false, "Print Telemetry data")
	outJSON        = flag.Bool("json", false, "Convert telemetry packet into JSON")
	logMux         = flag.Bool("log-mux-stdout", false, "All logs to stdout")
	maxRun         = flag.Int64("max-run", 0, "Max run time in seconds")
	stateHandler   = flag.Bool("stats-handler", false, "Use GRPC statshandler")
	versionOnly    = flag.Bool("version", false, "Print version and build-time of the binary and exit")
	compression    = flag.String("compression", "", "Enable HTTP/2 compression (gzip, deflate)")
	latencyProfile = flag.Bool("latency-profile", false, "Profile latencies. Place them in TSDB")
	prom           = flag.Bool("prometheus", false, "Stats for prometheus monitoring system")
	promPort       = flag.Int32("prometheus-port", 8090, "Prometheus port")
	prefixCheck    = flag.Bool("prefix-check", false, "Report missing __prefix__ in telemetry packet")
	apiControl     = flag.Bool("api", false, "Receive HTTP commands when running")
	pProf          = flag.Bool("pprof", false, "Profile JTIMON")
	pProfPort      = flag.Int32("pprof-port", 6060, "Profile port")
	gtrace         = flag.Bool("gtrace", false, "Collect GRPC traces")
	grpcHeaders    = flag.Bool("grpc-headers", false, "Add grpc headers in DB")
	noppgoroutines = flag.Bool("no-per-packet-goroutines", false, "Spawn per packet go routines")

	jtimonVersion = "version-not-available"
	buildTime     = "build-time-not-available"

	exporter *jtimonPExporter
)

func main() {
	flag.Parse()
	if *pProf {
		go func() {
			addr := fmt.Sprintf("localhost:%d", *pProfPort)
			log.Println(http.ListenAndServe(addr, nil))
		}()
	}
	if *prom {
		exporter = promInit()
	}
	startGtrace(*gtrace)

	log.Printf("Version: %s BuildTime %s\n", jtimonVersion, buildTime)
	if *versionOnly {
		return
	}

	if *expConfig {
		config, err := ExploreConfig()
		if err == nil {
			log.Printf("\n%s\n", config)
		} else {
			log.Printf("Can not generate config\n")
		}
		return
	}

	err := GetConfigFiles(cfgFile, cfgFileList)
	if err != nil {
		log.Printf("Config parsing error: %s \n", err)
		return
	}

	if *aliasFile != "" {
		aliasInit()
	}

	var wg sync.WaitGroup
	wMap := make(map[string]*workerCtx)

	for _, file := range *cfgFile {
		wg.Add(1)
		signalch, err := worker(file, &wg)
		if err != nil {
			wg.Done()
		} else {
			wMap[file] = &workerCtx{
				signalch: signalch,
				err:      err,
			}
		}
	}

	// Tell the workers (go routines) to actually start the work by Dialing GRPC connection
	// and send subscribe RPC.
	for _, wCtx := range wMap {
		if wCtx.err == nil {
			wCtx.signalch <- syscall.SIGCONT
		}
	}

	go signalHandler(*cfgFileList, wMap, &wg)
	go maxRunHandler(*maxRun, wMap)

	wg.Wait()
	log.Printf("All done ... exiting!\n")
}
