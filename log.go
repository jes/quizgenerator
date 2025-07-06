package quizgenerator

import "log"

// Global verbose flag
var verboseMode bool

// SetVerbose sets the global verbose mode
func SetVerbose(verbose bool) {
	verboseMode = verbose
}

// VerboseLog logs only when verbose mode is enabled
func VerboseLog(format string, v ...interface{}) {
	if verboseMode {
		log.Printf(format, v...)
	}
}
