//go:build windows
// +build windows

package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "version", "--version", "-version":
		fmt.Println(Version)
		return
	case "install":
		err = installCommand(os.Args[2:])
	case "remove":
		flags := flag.NewFlagSet("remove", flag.ContinueOnError)
		purge := flags.Bool("purge", false, "also delete configuration and logs")
		if parseErr := flags.Parse(os.Args[2:]); parseErr != nil {
			err = parseErr
		} else {
			err = removeService(*purge)
		}
	case "start", "stop", "status":
		err = serviceCommand(os.Args[1])
	case "service":
		flags := flag.NewFlagSet("service", flag.ContinueOnError)
		configPath := flags.String("config", defaultConfigPath(), "configuration path")
		if parseErr := flags.Parse(os.Args[2:]); parseErr != nil {
			err = parseErr
		} else {
			err = runWindowsService(*configPath)
		}
	case "run":
		err = runConsole(os.Args[2:])
	default:
		printUsage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "server-status-agent:", err)
		os.Exit(1)
	}
}

func installCommand(arguments []string) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	serverURL := flags.String("server", "", "central server URL")
	agentID := flags.String("id", "", "registered Agent UUID")
	token := flags.String("token", "", "node bearer token")
	environment := flags.String("environment", "production", "environment label")
	interval := flags.Int("interval", 60, "collection interval in seconds")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	config := Config{
		ServerURL: *serverURL, AgentID: *agentID, Token: *token,
		IntervalSeconds: *interval, Labels: map[string]string{"environment": *environment},
	}
	if err := config.validate(); err != nil {
		return err
	}
	if err := installService(config); err != nil {
		return err
	}
	fmt.Printf("Windows service %s installed and started.\n", serviceName)
	fmt.Printf("Configuration: %s\n", defaultConfigPath())
	return nil
}

func runConsole(arguments []string) error {
	flags := flag.NewFlagSet("run", flag.ContinueOnError)
	configPath := flags.String("config", defaultConfigPath(), "configuration path")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	config, err := loadConfig(*configPath)
	if err != nil {
		return err
	}
	stop := make(chan struct{})
	signals := make(chan os.Signal, 1)
	osSignalNotify(signals)
	go func() {
		<-signals
		close(stop)
	}()
	logger := log.New(os.Stdout, "", log.Ldate|log.Ltime|log.LUTC)
	logger.Printf("Agent started: version=%s server=%s", Version, config.ServerURL)
	return runAgent(stop, config, newWindowsCollector(), logger)
}

func osSignalNotify(signals chan<- os.Signal) {
	signal.Notify(signals, os.Interrupt)
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  server-status-agent.exe install --server URL --id UUID --token TOKEN [--environment production]")
	fmt.Fprintln(os.Stderr, "  server-status-agent.exe start|stop|status")
	fmt.Fprintln(os.Stderr, "  server-status-agent.exe remove [--purge]")
	fmt.Fprintln(os.Stderr, "  server-status-agent.exe run [--config PATH]")
	fmt.Fprintln(os.Stderr, "  server-status-agent.exe --version")
}
