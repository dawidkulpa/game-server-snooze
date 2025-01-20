package main

import (
	"github.com/sirupsen/logrus"
)

var (
	largestClientPacketSize int
	largestServerPacketSize int

	globalConfig Config
)

func setGlobalConfig(cfg Config) {
	globalConfig = cfg
	switch cfg.LogLevel {
	case "debug":
		logrus.SetLevel(logrus.DebugLevel)
	case "warn":
		logrus.SetLevel(logrus.WarnLevel)
	case "error":
		logrus.SetLevel(logrus.ErrorLevel)
	case "fatal":
		logrus.SetLevel(logrus.FatalLevel)
	default:
		logrus.SetLevel(logrus.InfoLevel)
	}
}

func updateLargestClientPacketSize(n int) {
	if n > largestClientPacketSize {
		largestClientPacketSize = n
		logrus.Debugf("Largest client packet size: %d", largestClientPacketSize)
	}
}

func updateLargestServerPacketSize(n int) {
	if n > largestServerPacketSize {
		largestServerPacketSize = n
		logrus.Debugf("Largest server packet size: %d", largestServerPacketSize)
	}
}
