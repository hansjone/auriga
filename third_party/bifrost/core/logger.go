// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package bifrost

import (
	"os"
	"sync"
	"time"

	schemas "github.com/maximhq/bifrost/core/schemas"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var zerologOnce sync.Once

type zerologEventBuilder struct {
	event *zerolog.Event
	msg   string
}
type NoOpLogger struct{}
type DefaultLogger struct {
	stderrLogger zerolog.Logger
	stdoutLogger zerolog.Logger
}

func toZerologLevel(l schemas.LogLevel) zerolog.Level {
	switch l {
	case schemas.LogLevelDebug:
		return zerolog.DebugLevel
	case schemas.LogLevelInfo:
		return zerolog.InfoLevel
	case schemas.LogLevelWarn:
		return zerolog.WarnLevel
	case schemas.LogLevelError:
		return zerolog.ErrorLevel
	default:
		return zerolog.InfoLevel
	}
}

func NewDefaultLogger(level schemas.LogLevel) *DefaultLogger {
	zerolog.SetGlobalLevel(toZerologLevel(level))
	zerologOnce.Do(func() {
		zerolog.DisableSampling(true)
		zerolog.TimeFieldFormat = time.RFC3339
		log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()
	})
	return &DefaultLogger{
		stderrLogger: zerolog.New(os.Stderr).With().Timestamp().Logger(),
		stdoutLogger: zerolog.New(os.Stdout).With().Timestamp().Logger(),
	}
}

func (logger *DefaultLogger) Debug(msg string, args ...any) {
	logger.stdoutLogger.Debug().Msgf(msg, args...)
}

func (logger *DefaultLogger) Info(msg string, args ...any) {
	logger.stdoutLogger.Info().Msgf(msg, args...)
}

func (logger *DefaultLogger) Warn(msg string, args ...any) {
	logger.stdoutLogger.Warn().Msgf(msg, args...)
}

func (logger *DefaultLogger) Error(msg string, args ...any) {
	logger.stderrLogger.Error().Msgf(msg, args...)
}

func (logger *DefaultLogger) Fatal(msg string, args ...any) {

	var errToPass error
	for i, arg := range args {
		if err, ok := arg.(error); ok && err != nil {
			errToPass = err

			args = append(args[:i], args[i+1:]...)
		}
	}
	if errToPass != nil {
		logger.stderrLogger.Fatal().Msgf(msg, errToPass)
	} else {
		logger.stderrLogger.Fatal().Msgf(msg, args...)
	}
}

func (logger *DefaultLogger) SetLevel(level schemas.LogLevel) {
	zerolog.SetGlobalLevel(toZerologLevel(level))
}

func (logger *DefaultLogger) SetOutputType(outputType schemas.LoggerOutputType) {
	switch outputType {
	case schemas.LoggerOutputTypePretty:
		logger.stdoutLogger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stdout}).With().Timestamp().Logger()
		logger.stderrLogger = zerolog.New(zerolog.ConsoleWriter{Out: os.Stderr}).With().Timestamp().Logger()
	case schemas.LoggerOutputTypeJSON:
		logger.stdoutLogger = zerolog.New(os.Stdout).With().Timestamp().Logger()
		logger.stderrLogger = zerolog.New(os.Stderr).With().Timestamp().Logger()
	default:
		logger.stderrLogger.Warn().
			Str("outputType", string(outputType)).
			Msg("unknown logger output type; defaulting to JSON")
		logger.stdoutLogger = zerolog.New(os.Stdout).With().Timestamp().Logger()
	}
}

func (l *NoOpLogger) Debug(string, ...any)                   {}
func (l *NoOpLogger) Info(string, ...any)                    {}
func (l *NoOpLogger) Warn(string, ...any)                    {}
func (l *NoOpLogger) Error(string, ...any)                   {}
func (l *NoOpLogger) Fatal(string, ...any)                   {}
func (l *NoOpLogger) SetLevel(schemas.LogLevel)              {}
func (l *NoOpLogger) SetOutputType(schemas.LoggerOutputType) {}
func (l *NoOpLogger) LogHTTPRequest(schemas.LogLevel, string) schemas.LogEventBuilder {
	return schemas.NoopLogEvent
}

func (b *zerologEventBuilder) Str(key, val string) schemas.LogEventBuilder {
	b.event = b.event.Str(key, val)
	return b
}

func (b *zerologEventBuilder) Int(key string, val int) schemas.LogEventBuilder {
	b.event = b.event.Int(key, val)
	return b
}

func (b *zerologEventBuilder) Int64(key string, val int64) schemas.LogEventBuilder {
	b.event = b.event.Int64(key, val)
	return b
}

func (b *zerologEventBuilder) Send() {
	b.event.Msg(b.msg)
}

func (logger *DefaultLogger) LogHTTPRequest(level schemas.LogLevel, msg string) schemas.LogEventBuilder {
	l := logger.stdoutLogger
	if level == schemas.LogLevelError {
		l = logger.stderrLogger
	}
	var event *zerolog.Event
	switch level {
	case schemas.LogLevelDebug:
		event = l.Debug()
	case schemas.LogLevelWarn:
		event = l.Warn()
	case schemas.LogLevelError:
		event = l.Error()
	default:
		event = l.Info()
	}
	return &zerologEventBuilder{event: event, msg: msg}
}
