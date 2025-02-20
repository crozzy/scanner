// +build db_integration

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

package pgsql

import (
	"math/rand"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/pborman/uuid"
	log "github.com/sirupsen/logrus"
	"github.com/stackrox/scanner/database"
	"github.com/stackrox/scanner/ext/versionfmt/dpkg"
	"github.com/stretchr/testify/assert"
)

const (
	numVulnerabilities = 100
	numFeatureVersions = 100

	searchComplexTestFeatureVersionAffects = `
		SELECT v.name
    FROM FeatureVersion fv
      LEFT JOIN Vulnerability_Affects_FeatureVersion vaf ON fv.id = vaf.featureversion_id
      JOIN Vulnerability v ON vaf.vulnerability_id = v.id
    WHERE featureversion_id = $1`
)

func TestRaceAffects(t *testing.T) {
	datastore, err := openDatabaseForTest("RaceAffects", false)
	if err != nil {
		t.Error(err)
		return
	}
	defer datastore.Close()

	// Insert the Feature on which we'll work.
	feature := database.Feature{
		Namespace: database.Namespace{
			Name:          "TestRaceAffectsFeatureNamespace1",
			VersionFormat: dpkg.ParserName,
		},
		Name: "TestRaceAffecturesFeature1",
	}
	_, err = datastore.insertFeature(feature)
	if err != nil {
		t.Error(err)
		return
	}

	// Initialize random generator and enforce max procs.
	rand.Seed(time.Now().UnixNano())
	runtime.GOMAXPROCS(runtime.NumCPU())

	// Generate FeatureVersions.
	featureVersions := make([]database.FeatureVersion, numFeatureVersions)
	for i := 0; i < numFeatureVersions; i++ {
		version := rand.Intn(numFeatureVersions)

		featureVersions[i] = database.FeatureVersion{
			Feature: feature,
			Version: strconv.Itoa(version),
		}
	}

	// Generate vulnerabilities.
	// They are mapped by fixed version, which will make verification really easy afterwards.
	vulnerabilities := make(map[int][]database.Vulnerability)
	for i := 0; i < numVulnerabilities; i++ {
		version := rand.Intn(numFeatureVersions) + 1

		// if _, ok := vulnerabilities[version]; !ok {
		//   vulnerabilities[version] = make([]database.Vulnerability)
		// }

		vulnerability := database.Vulnerability{
			Name:      uuid.New(),
			Namespace: feature.Namespace,
			FixedIn: []database.FeatureVersion{
				{
					Feature: feature,
					Version: strconv.Itoa(version),
				},
			},
			Severity: database.UnknownSeverity,
		}

		vulnerabilities[version] = append(vulnerabilities[version], vulnerability)
	}

	// Insert featureversions and vulnerabilities in parallel.
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for _, vulnerabilitiesM := range vulnerabilities {
			for _, vulnerability := range vulnerabilitiesM {
				err = datastore.InsertVulnerabilities([]database.Vulnerability{vulnerability})
				assert.Nil(t, err)
			}
		}
		log.Info("Finished inserting vulnerabilities")
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < len(featureVersions); i++ {
			featureVersions[i].ID, err = datastore.insertFeatureVersion(featureVersions[i])
			assert.Nil(t, err)
		}
		log.Info("finished inserting featureVersions")
	}()

	wg.Wait()

	// Verify consistency now.
	var actualAffectedNames []string
	var expectedAffectedNames []string

	for _, featureVersion := range featureVersions {
		featureVersionVersion, _ := strconv.Atoi(featureVersion.Version)

		// Get actual affects.
		rows, err := datastore.Query(searchComplexTestFeatureVersionAffects,
			featureVersion.ID)
		assert.Nil(t, err)
		defer rows.Close()

		var vulnName string
		for rows.Next() {
			err = rows.Scan(&vulnName)
			if !assert.Nil(t, err) {
				continue
			}
			actualAffectedNames = append(actualAffectedNames, vulnName)
		}
		if assert.Nil(t, rows.Err()) {
			rows.Close()
		}

		// Get expected affects.
		for i := numVulnerabilities; i > featureVersionVersion; i-- {
			for _, vulnerability := range vulnerabilities[i] {
				expectedAffectedNames = append(expectedAffectedNames, vulnerability.Name)
			}
		}

		assert.Len(t, compareStringLists(expectedAffectedNames, actualAffectedNames), 0)
		assert.Len(t, compareStringLists(actualAffectedNames, expectedAffectedNames), 0)
	}
}
