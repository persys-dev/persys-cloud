package logging

import (
	"os"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

var (
	baseLogger *logrus.Logger
	initOnce   sync.Once
)

func Init() *logrus.Logger {
	initOnce.Do(func() {
		l := logrus.New()
		l.SetOutput(os.Stdout)

		switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT"))) {
		case "", "text":
			l.SetFormatter(&logrus.TextFormatter{
				FullTimestamp:          true,
				TimestampFormat:        "2006-01-02T15:04:05-07:00",
				ForceColors:            true,
				DisableColors:          false,
				PadLevelText:           true,
				DisableLevelTruncation: true,
			})
		case "json":
			l.SetFormatter(&logrus.JSONFormatter{})
		default:
			l.SetFormatter(&logrus.TextFormatter{
				FullTimestamp:          true,
				TimestampFormat:        "2006-01-02T15:04:05-07:00",
				ForceColors:            true,
				DisableColors:          false,
				PadLevelText:           true,
				DisableLevelTruncation: true,
			})
		}

		level := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL")))
		if level == "" {
			level = "info"
		}
		parsedLevel, err := logrus.ParseLevel(level)
		if err != nil {
			parsedLevel = logrus.InfoLevel
		}
		l.SetLevel(parsedLevel)

		baseLogger = l
	})

	return baseLogger
}

func L() *logrus.Logger {
	if baseLogger == nil {
		return Init()
	}
	return baseLogger
}

func C(component string) *logrus.Entry {
	return L().WithField("component", component)
}
