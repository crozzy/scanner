// +build db_integration

// Copyright 2016 clair authors
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

package pgsql

import (
	"testing"

	"github.com/stackrox/scanner/database"
	"github.com/stackrox/scanner/ext/versionfmt/dpkg"
	"github.com/stretchr/testify/assert"
)

func TestInsertFeature(t *testing.T) {
	datastore, err := openDatabaseForTest("InsertFeature", false)
	if err != nil {
		t.Error(err)
		return
	}
	defer datastore.Close()

	// Invalid Feature.
	id0, err := datastore.insertFeature(database.Feature{})
	assert.NotNil(t, err)
	assert.Zero(t, id0)

	id0, err = datastore.insertFeature(database.Feature{
		Namespace: database.Namespace{},
		Name:      "TestInsertFeature0",
	})
	assert.NotNil(t, err)
	assert.Zero(t, id0)

	// Insert Feature and ensure we can find it.
	feature := database.Feature{
		Namespace: database.Namespace{
			Name:          "TestInsertFeatureNamespace1",
			VersionFormat: dpkg.ParserName,
		},
		Name: "TestInsertFeature1",
	}
	id1, err := datastore.insertFeature(feature)
	assert.Nil(t, err)
	id2, err := datastore.insertFeature(feature)
	assert.Nil(t, err)
	assert.Equal(t, id1, id2)

	// Insert invalid FeatureVersion.
	for _, invalidFeatureVersion := range []database.FeatureVersion{
		{
			Feature: database.Feature{},
			Version: "1.0",
		},
		{
			Feature: database.Feature{
				Namespace: database.Namespace{},
				Name:      "TestInsertFeature2",
			},
			Version: "1.0",
		},
		{
			Feature: database.Feature{
				Namespace: database.Namespace{
					Name:          "TestInsertFeatureNamespace2",
					VersionFormat: dpkg.ParserName,
				},
				Name: "TestInsertFeature2",
			},
			Version: "",
		},
		{
			Feature: database.Feature{
				Namespace: database.Namespace{
					Name:          "TestInsertFeatureNamespace2",
					VersionFormat: dpkg.ParserName,
				},
				Name: "TestInsertFeature2",
			},
			Version: "bad version",
		},
	} {
		id3, err := datastore.insertFeatureVersion(invalidFeatureVersion)
		assert.Error(t, err)
		assert.Zero(t, id3)
	}

	// Insert FeatureVersion and ensure we can find it.
	featureVersion := database.FeatureVersion{
		Feature: database.Feature{
			Namespace: database.Namespace{
				Name:          "TestInsertFeatureNamespace1",
				VersionFormat: dpkg.ParserName,
			},
			Name: "TestInsertFeature1",
		},
		Version: "2:3.0-imba",
		ExecutableToDependencies: database.StringToStringsMap{
			"/bin/ls": {"some.so.1": {}},
			"/bin/ps": {},
		},
		LibraryToDependencies: database.StringToStringsMap{"linux.so.1": {"libxml.so.2": {}}},
	}
	id4, err := datastore.insertFeatureVersion(featureVersion)
	assert.Nil(t, err)
	id5, err := datastore.insertFeatureVersion(featureVersion)
	assert.Nil(t, err)
	assert.Equal(t, id4, id5)
}
