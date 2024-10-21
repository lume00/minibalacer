package internal

import (
	"encoding/json"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path"
	"sync"
)

const CONF_FILE_NAME string = "conf.json"

var runningConf *Conf

type Global struct {
	Logger *Logger `json:"logger"`
}

func (global *Global) Stop() {
	global.Logger.Stop()
}

func (global *Global) Start() error {
	return global.Logger.Start()
}

type Conf struct {
	Settings        *LoadBalancerSettings `json:"settings"`
	Global          *Global               `json:"global"`
	BasePath        string                `json:"basePath"`
	interruptSignal chan os.Signal        `json:"-"`
	Wg              *sync.WaitGroup       `json:"-"`
}

// Initializing Conf. Reading conf file (default is ./conf.json)
// but can be overriden by passing `--conf confpath`
func FromFile() error {

	confPath := flag.String("conf", CONF_FILE_NAME, "configuration file path")
	flag.Parse()

	read, err := os.ReadFile(*confPath)
	//if file not found
	if err != nil {
		return err
	}

	pathDir := path.Dir(*confPath)
	conf := &Conf{BasePath: pathDir}

	err = json.Unmarshal(read, conf)

	if err != nil {
		slog.Error("error during file unmarshal", "error", err)
		return err
	}

	err = conf.Start()

	if err != nil {
		slog.Error("error during initialization from file", "error", err)
		return err
	}

	conf.Wg = &sync.WaitGroup{}
	conf.Wg.Add(1)
	conf.Wg.Wait()

	return nil
}

func (conf *Conf) Start() error {
	runningConf = conf

	e := conf.Global.Start()
	if e != nil {
		return e
	}

	// e = conf.Api.Start()
	// if e != nil {
	// 	return e
	// }

	conf.startInterruptSignalReceiver()
	e = conf.Settings.Start()

	if e != nil {
		return e
	}
	return nil
}

func (conf *Conf) Stop() {
	close(conf.interruptSignal)
	// conf.Api.Stop()
	conf.Settings.Stop()
	conf.Global.Stop()
}

func (conf *Conf) startInterruptSignalReceiver() {
	osInterrupt := make(chan os.Signal, 1)
	conf.interruptSignal = osInterrupt
	signal.Notify(osInterrupt, os.Interrupt)
	go func() {
		signal := <-osInterrupt
		if signal != nil {
			// conf.Api.Stop()
			conf.Settings.Stop()
			conf.Global.Stop()
			conf.Wg.Done()
		}
	}()
}
