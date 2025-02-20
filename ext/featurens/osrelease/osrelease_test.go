// Copyright 2017 clair authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package osrelease

import (
	"testing"

	"github.com/stackrox/scanner/database"
	"github.com/stackrox/scanner/ext/featurens"
	"github.com/stackrox/scanner/ext/versionfmt/dpkg"
	"github.com/stackrox/scanner/pkg/tarutil"
)

func TestDetector(t *testing.T) {
	testData := []featurens.TestData{
		{
			ExpectedNamespace: &database.Namespace{Name: "debian:8", VersionFormat: dpkg.ParserName},
			Files: tarutil.CreateNewLayerFiles(map[string]tarutil.FileData{
				"etc/os-release": {Contents: []byte(
					`PRETTY_NAME="Debian GNU/Linux 8 (jessie)"
NAME="Debian GNU/Linux"
VERSION_ID="8"
VERSION="8 (jessie)"
ID=debian
HOME_URL="http://www.debian.org/"
SUPPORT_URL="http://www.debian.org/support/"
BUG_REPORT_URL="https://bugs.debian.org/"`)},
			}),
		},
		{
			ExpectedNamespace: &database.Namespace{Name: "ubuntu:15.10", VersionFormat: dpkg.ParserName},
			Files: tarutil.CreateNewLayerFiles(map[string]tarutil.FileData{
				"etc/os-release": {Contents: []byte(
					`NAME="Ubuntu"
VERSION="15.10 (Wily Werewolf)"
ID=ubuntu
ID_LIKE=debian
PRETTY_NAME="Ubuntu Wily Werewolf (development branch)"
VERSION_ID="15.10"
HOME_URL="http://www.ubuntu.com/"
SUPPORT_URL="http://help.ubuntu.com/"
BUG_REPORT_URL="http://bugs.launchpad.net/ubuntu/"`)},
			}),
		},
		{ // Doesn't have quotes around VERSION_ID
			ExpectedNamespace: &database.Namespace{Name: "ubuntu:15.10", VersionFormat: dpkg.ParserName},
			Files: tarutil.CreateNewLayerFiles(map[string]tarutil.FileData{
				"etc/os-release": {Contents: []byte(
					`NAME="Ubuntu"
ID=ubuntu
ID_LIKE=debian
PRETTY_NAME="Ubuntu Wily Werewolf (development branch)"
VERSION_ID=15.10
HOME_URL="http://www.ubuntu.com/"
SUPPORT_URL="http://help.ubuntu.com/"
BUG_REPORT_URL="http://bugs.launchpad.net/ubuntu/"`)},
			}),
		},
		{ // We do not support Fedora.
			ExpectedNamespace: nil,
			Files: tarutil.CreateNewLayerFiles(map[string]tarutil.FileData{
				"etc/os-release": {Contents: []byte(
					`NAME=Fedora
VERSION="20 (Heisenbug)"
ID=fedora
VERSION_ID=20
PRETTY_NAME="Fedora 20 (Heisenbug)"
ANSI_COLOR="0;34"
CPE_NAME="cpe:/o:fedoraproject:fedora:20"
HOME_URL="https://fedoraproject.org/"
BUG_REPORT_URL="https://bugzilla.redhat.com/"
REDHAT_BUGZILLA_PRODUCT="Fedora"
REDHAT_BUGZILLA_PRODUCT_VERSION=20
REDHAT_SUPPORT_PRODUCT="Fedora"
REDHAT_SUPPORT_PRODUCT_VERSION=20`)},
			}),
		},
		{
			ExpectedNamespace: nil,
			Files:             tarutil.CreateNewLayerFiles(nil),
		},
	}

	featurens.TestDetector(t, &detector{}, testData)
}
