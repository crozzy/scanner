package cos

import (
	"strings"

	"github.com/stackrox/scanner/database"
	"github.com/stackrox/scanner/ext/kernelparser"
)

func init() {
	kernelparser.RegisterParser("cos", parser)
}

func parser(_ database.Datastore, kernelVersion, osImage string) (*kernelparser.ParseMatch, bool, error) {
	if strings.HasSuffix(kernelVersion, "+") && strings.Contains(osImage, "container-optimized") {
		return nil, true, nil
	}
	return nil, false, nil
}
