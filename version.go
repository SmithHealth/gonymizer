package gonymizer

import "time"

const (
	buildTimeStamp = 1564524673
	buildNumber    = 8
	buildVersion   = "1.1.4"
)

// BuildDate will return the current unix time as the build date time for the application.
func BuildDate() time.Time {
	return time.Unix(buildTimeStamp, 0)
}

// BuildNumber will return the build number for the application.
func BuildNumber() int64 {
	return buildNumber
}

// Version will return the version number for the application.
func Version() string {
	return buildVersion
}
