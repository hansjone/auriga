// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package schemas

type Logger interface {
	Debug(msg string, args ...any)

	Info(msg string, args ...any)

	Warn(msg string, args ...any)

	Error(msg string, args ...any)

	Fatal(msg string, args ...any)

	SetLevel(level LogLevel)

	SetOutputType(outputType LoggerOutputType)

	LogHTTPRequest(level LogLevel, msg string) LogEventBuilder
}
type LogLevel string
type noopLogEventBuilder struct{}
type LogEventBuilder interface {
	Str(key, val string) LogEventBuilder
	Int(key string, val int) LogEventBuilder
	Int64(key string, val int64) LogEventBuilder
	Send()
}

var NoopLogEvent LogEventBuilder = noopLogEventBuilder{}

type LoggerOutputType string

const (
	LogLevelDebug LogLevel = "debug"
	LogLevelInfo  LogLevel = "info"
	LogLevelWarn  LogLevel = "warn"
	LogLevelError LogLevel = "error"
)

const (
	LoggerOutputTypeJSON   LoggerOutputType = "json"
	LoggerOutputTypePretty LoggerOutputType = "pretty"
)

func (noopLogEventBuilder) Str(string, string) LogEventBuilder  { return noopLogEventBuilder{} }
func (noopLogEventBuilder) Int(string, int) LogEventBuilder     { return noopLogEventBuilder{} }
func (noopLogEventBuilder) Int64(string, int64) LogEventBuilder { return noopLogEventBuilder{} }
func (noopLogEventBuilder) Send()                               {}
