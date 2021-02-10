package main

//go:generate go run v2ray.com/core/common/errors/errorgen

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"v2ray.com/core"
	"v2ray.com/core/common/cmdarg"
	"v2ray.com/core/common/platform"
	"v2ray.com/core/infra/conf"
	_ "v2ray.com/core/main/distro/all"
)

var (
	configFiles cmdarg.Arg // "Config file for V2Ray.", the option is customed type, parse in main
	configDir   string
	version     = flag.Bool("version", false, "Show current version of V2Ray.")
	test        = flag.Bool("test", false, "Test config file only, without launching V2Ray server.")
	format      = flag.String("format", "json", "Format of input file.")

	inline                = flag.Bool("inline", false, "Indicate a simple VMess outbound and a SOCKS5 inbound")
	inlinePort            = flag.Int("port", 1080, "When inline is true, indicate the SOCKS5 inbound's listening port")
	inlineUDP             = flag.Bool("udp", true, "When inline is true, indicate whether the SOCKS5 inbound supports UDP")
	inlineLocalIP         = flag.String("local-ip", "127.0.0.1", "When inline is true, indicate the SOCKS5 inbound's local IP")
	inlineVMessAddr       = flag.String("vmess-addr", "", "When inline is true, indicate the VMess outbound's address")
	inlineVMessPort       = flag.Int("vmess-port", 0, "When inline is true, indicate the VMess outbound's port")
	inlineVMessID         = flag.String("vmess-id", "", "When inline is true, indicate the VMess outbound's user ID")
	inlineVMessAlterID    = flag.Int("vmess-alter-id", 0, "When inline is true, indicate the VMess outbound's user AlterID")
	inlineVMessNetwork    = flag.String("vmess-network", "tcp", "When inline is true, indicate the VMess outbound's network")
	inlineVMessTLS        = flag.Bool("vmess-tls", false, "When inline is true, indicate whether the VMess outbound used TLS")
	inlineVMessWSPath     = flag.String("vmess-ws-path", "/ws", "When inline is true and vmess-network is ws, indicate the VMess outbound's WebSocket path")
	inlineVMessWSServName = flag.String("vmess-ws-servname", "", "When inline is true and vmess-tls is true, indicate the server name.")

	/* We have to do this here because Golang's Test will also need to parse flag, before
	 * main func in this file is run.
	 */
	_ = func() error { // nolint: unparam
		flag.Var(&configFiles, "config", "Config file for V2Ray. Multiple assign is accepted (only json). Latter ones overrides the former ones.")
		flag.Var(&configFiles, "c", "Short alias of -config")
		flag.StringVar(&configDir, "confdir", "", "A dir with multiple json config")

		return nil
	}()
)

func fileExists(file string) bool {
	info, err := os.Stat(file)
	return err == nil && !info.IsDir()
}

func dirExists(file string) bool {
	if file == "" {
		return false
	}
	info, err := os.Stat(file)
	return err == nil && info.IsDir()
}

func readConfDir(dirPath string) {
	confs, err := ioutil.ReadDir(dirPath)
	if err != nil {
		log.Fatalln(err)
	}
	for _, f := range confs {
		if strings.HasSuffix(f.Name(), ".json") {
			configFiles.Set(path.Join(dirPath, f.Name()))
		}
	}
}

func getConfigFilePath() cmdarg.Arg {
	if dirExists(configDir) {
		log.Println("Using confdir from arg:", configDir)
		readConfDir(configDir)
	} else if envConfDir := platform.GetConfDirPath(); dirExists(envConfDir) {
		log.Println("Using confdir from env:", envConfDir)
		readConfDir(envConfDir)
	}

	if len(configFiles) > 0 {
		return configFiles
	}

	if workingDir, err := os.Getwd(); err == nil {
		configFile := filepath.Join(workingDir, "config.json")
		if fileExists(configFile) {
			log.Println("Using default config: ", configFile)
			return cmdarg.Arg{configFile}
		}
	}

	if configFile := platform.GetConfigurationPath(); fileExists(configFile) {
		log.Println("Using config from env: ", configFile)
		return cmdarg.Arg{configFile}
	}

	log.Println("Using config from STDIN")
	return cmdarg.Arg{"stdin:"}
}

func GetConfigFormat() string {
	switch strings.ToLower(*format) {
	case "pb", "protobuf":
		return "protobuf"
	default:
		return "json"
	}
}

func getConfig() (*core.Config, error) {
	if *inline {
		if *inlineVMessAddr == "" {
			return nil, newError("-vmess-addr is required when inline mode is on")
		}
		if *inlineVMessID == "" {
			return nil, newError("-vmess-id is required when inline mode is on")
		}
		if *inlineVMessPort == 0 {
			return nil, newError("-vmess-port is required when inline mode is on")
		}
		if *inlineVMessAlterID == 0 {
			return nil, newError("-vmess-alter-id is required when inline mode is on")
		}

		type (
			M map[string]interface{}
			D []interface{}
		)
		streamSettings := M{}
		security := "none"
		if *inlineVMessTLS {
			security = "tls"
		}
		if *inlineVMessNetwork == "ws" {
			streamSettings = M{
				"network":  "ws",
				"security": security,
				"wsSettings": M{
					"path": *inlineVMessWSPath,
				},
			}
		} else {
			streamSettings = M{
				"network":  "tcp",
				"security": security,
			}
		}
		if *inlineVMessTLS && *inlineVMessWSServName != "" {
			streamSettings["tlsSettings"] = M{
				"serverName": *inlineVMessWSServName,
			}
		}
		mConf := M{
			"inbounds": D{
				M{
					"port":     *inlinePort,
					"listen":   "127.0.0.1",
					"protocol": "socks",
					"settings": M{
						"auth":      "noauth",
						"udp":       *inlineUDP,
						"ip":        *inlineLocalIP,
						"userLevel": 0,
					},
				},
			},
			"outbounds": D{
				M{
					"protocol": "vmess",
					"settings": M{
						"vnext": D{
							M{
								"address": *inlineVMessAddr,
								"port":    *inlineVMessPort,
								"users": D{
									M{
										"id":      *inlineVMessID,
										"alterId": *inlineVMessAlterID,
										"level":   0,
									},
								},
							},
						},
					},
					"streamSettings": streamSettings,
				},
			},
		}
		bConf, err := json.Marshal(mConf)
		if err != nil {
			panic(fmt.Errorf("failed to marshal conf: %v", err))
		}
		cfConf := &conf.Config{}
		err = json.Unmarshal(bConf, &cfConf)
		if err != nil {
			panic(fmt.Errorf("failed to unmarshal conf: %v", err))
		}
		coreConf, err := cfConf.Build()
		if err != nil {
			panic(fmt.Errorf("failed to build conf: %v", err))
		}
		return coreConf, nil
	}

	configFiles := getConfigFilePath()

	config, err := core.LoadConfig(GetConfigFormat(), configFiles[0], configFiles)
	if err != nil {
		return nil, newError("failed to read config files: [", configFiles.String(), "]").Base(err)
	}

	return config, nil
}

func startV2Ray() (core.Server, error) {
	config, err := getConfig()
	if err != nil {
		return nil, err
	}

	server, err := core.New(config)
	if err != nil {
		return nil, newError("failed to create server").Base(err)
	}

	return server, nil
}

func printVersion() {
	version := core.VersionStatement()
	for _, s := range version {
		fmt.Println(s)
	}
}

func main() {
	flag.Parse()

	printVersion()

	if *version {
		return
	}

	server, err := startV2Ray()
	if err != nil {
		fmt.Println(err)
		// Configuration error. Exit with a special value to prevent systemd from restarting.
		os.Exit(23)
	}

	if *test {
		fmt.Println("Configuration OK.")
		os.Exit(0)
	}

	if err := server.Start(); err != nil {
		fmt.Println("Failed to start", err)
		os.Exit(-1)
	}
	defer server.Close()

	// Explicitly triggering GC to remove garbage from config loading.
	runtime.GC()

	{
		osSignals := make(chan os.Signal, 1)
		signal.Notify(osSignals, os.Interrupt, syscall.SIGTERM)
		<-osSignals
	}
}
