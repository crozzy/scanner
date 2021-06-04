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

package clair

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/stackrox/scanner/database"
	"github.com/stackrox/scanner/ext/featurefmt"
	"github.com/stackrox/scanner/ext/featurens"
	"github.com/stackrox/scanner/ext/imagefmt"
	"github.com/stackrox/scanner/pkg/analyzer"
	"github.com/stackrox/scanner/pkg/commonerr"
	"github.com/stackrox/scanner/pkg/component"
	"github.com/stackrox/scanner/pkg/env"
	featureFlags "github.com/stackrox/scanner/pkg/features"
	rhelv2 "github.com/stackrox/scanner/pkg/rhelv2/rpm"
	"github.com/stackrox/scanner/pkg/tarutil"
	namespaces "github.com/stackrox/scanner/pkg/wellknownnamespaces"
	"github.com/stackrox/scanner/pkg/whiteout"
	"github.com/stackrox/scanner/singletons/analyzers"
	"github.com/stackrox/scanner/singletons/requiredfilenames"
)

const (
	// Version (integer) represents the worker version.
	// Increased each time the engine changes.
	Version      = 3
	logLayerName = "layer"
)

var (
	// ErrUnsupported is the error that should be raised when an OS or package
	// manager is not supported.
	ErrUnsupported = commonerr.NewBadRequestError("worker: OS and/or package manager are not supported")

	// ErrParentUnknown is the error that should be raised when a parent layer
	// has yet to be processed for the current layer.
	ErrParentUnknown = commonerr.NewBadRequestError("worker: parent layer is unknown, it must be processed first")
)

func preProcessLayer(datastore database.Datastore, imageFormat, name, lineage, parentName, parentLineage string, uncertifiedRHEL bool) (database.Layer, bool, error) {
	// Verify parameters.
	if name == "" {
		return database.Layer{}, false, commonerr.NewBadRequestError("could not process a layer which does not have a name")
	}

	if imageFormat == "" {
		return database.Layer{}, false, commonerr.NewBadRequestError("could not process a layer which does not have a format")
	}

	// Check to see if the layer is already in the database.
	layer, err := datastore.FindLayer(name, lineage, &database.DatastoreOptions{
		UncertifiedRHEL: uncertifiedRHEL,
	})
	if err != nil && err != commonerr.ErrNotFound {
		return layer, false, err
	}

	if err == commonerr.ErrNotFound {
		// New layer case.
		layer = database.Layer{Name: name, EngineVersion: Version}

		// Retrieve the parent if it has one.
		// We need to get it with its Features in order to diff them.
		if parentName != "" {
			// rolling hash of parents up to point
			parent, err := datastore.FindLayer(parentName, parentLineage, &database.DatastoreOptions{
				WithFeatures:    true,
				UncertifiedRHEL: uncertifiedRHEL,
			})
			if err != nil && err != commonerr.ErrNotFound {
				return layer, false, err
			}
			if err == commonerr.ErrNotFound {
				log.WithFields(log.Fields{logLayerName: name, "parent layer": parentName}).Warning("the parent layer is unknown. it must be processed first")
				return layer, false, ErrParentUnknown
			}
			layer.Parent = &parent
		}
		return layer, false, nil
	}
	// The layer is already in the database, check if we need to update it.
	if layer.EngineVersion >= Version {
		log.WithFields(log.Fields{logLayerName: name, "past engine version": layer.EngineVersion, "current engine version": Version}).Debug("layer content has already been processed in the past with older engine. skipping analysis")
		return layer, true, nil
	}
	log.WithFields(log.Fields{logLayerName: name, "past engine version": layer.EngineVersion, "current engine version": Version}).Debug("layer content has already been processed in the past with older engine. analyzing again")
	return layer, false, nil
}

// ProcessLayerFromReader detects the Namespace of a layer, the features it adds/removes,
// and then stores everything in the database.
//
// TODO(Quentin-M): We could have a goroutine that looks for layers that have
// been analyzed with an older engine version and that processes them.
func ProcessLayerFromReader(datastore database.Datastore, imageFormat, name, lineage, parentName, parentLineage string, reader io.ReadCloser, uncertifiedRHEL bool) error {
	layer, exists, err := preProcessLayer(datastore, imageFormat, name, lineage, parentName, parentLineage, uncertifiedRHEL)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	// Analyze the content.
	var rhelv2Components *database.RHELv2Components
	var languageComponents []*component.Component
	var removedFiles []string
	layer.Namespace, layer.Distroless, layer.Features, rhelv2Components, languageComponents, removedFiles, err = DetectContentFromReader(reader, imageFormat, name, layer.Parent, uncertifiedRHEL)
	if err != nil {
		return err
	}

	if rhelv2Components != nil {
		// Go this path for Red Hat Certified scans.
		var parentHash string
		if layer.Parent != nil && layer.Parent.Name != "" {
			parentHash = layer.Parent.Name
		}
		rhelv2Layer := &database.RHELv2Layer{
			Hash:       layer.Name,
			Dist:       rhelv2Components.Dist,
			Pkgs:       rhelv2Components.Packages,
			CPEs:       rhelv2Components.CPEs,
			ParentHash: parentHash,
		}

		if err := datastore.InsertRHELv2Layer(rhelv2Layer); err != nil {
			return err
		}
	}

	opts := &database.DatastoreOptions{
		UncertifiedRHEL: uncertifiedRHEL,
	}

	// This is required for RHEL-base images as well, as language vuln scanning
	// relies on the original layer table.
	if err := datastore.InsertLayer(layer, lineage, opts); err != nil {
		if err == commonerr.ErrNoNeedToInsert {
			return nil
		}
		return err
	}

	return datastore.InsertLayerComponents(layer.Name, lineage, languageComponents, removedFiles, opts)
}

func detectFromFiles(files tarutil.FilesMap, name string, parent *database.Layer, uncertifiedRHEL bool) (*database.Namespace, bool, []database.FeatureVersion, *database.RHELv2Components, []*component.Component, []string, error) {
	namespace := DetectNamespace(name, files, parent, uncertifiedRHEL)

	distroless := isDistroless(files) || (parent != nil && parent.Distroless)

	var featureVersions []database.FeatureVersion
	var rhelfeatures *database.RHELv2Components

	languageAnalyzerFunc := analyzer.Analyze
	if namespace != nil && namespaces.IsRHELNamespace(namespace.Name) {
		// This is a RHEL-based image that must be scanned in a certified manner.
		// Use the RHELv2 scanner instead.
		packages, cpes, err := rhelv2.ListFeatures(files)
		if err != nil {
			return nil, distroless, nil, nil, nil, nil, err
		}
		rhelfeatures = &database.RHELv2Components{
			Dist:     namespace.Name,
			Packages: packages,
			CPEs:     cpes,
		}
		log.WithFields(log.Fields{logLayerName: name, "rhel package count": len(packages), "rhel cpe count": len(cpes)}).Debug("detected rhelv2 features")
		languageAnalyzerFunc = rhelv2.WrapAnalyzer()
	} else {
		var err error
		// Detect features.
		featureVersions, err = detectFeatureVersions(name, files, namespace, parent)
		if err != nil {
			return nil, distroless, nil, nil, nil, nil, err
		}
		if len(featureVersions) > 0 {
			log.WithFields(log.Fields{logLayerName: name, "feature count": len(featureVersions)}).Debug("detected features")
		}
	}

	if !env.LanguageVulns.Enabled() {
		return namespace, distroless, featureVersions, rhelfeatures, nil, nil, nil
	}
	allComponents, err := languageAnalyzerFunc(files, analyzers.Analyzers())
	if err != nil {
		log.WithError(err).Errorf("Failed to analyze image: %s", name)
	}
	if len(allComponents) > 0 {
		log.WithFields(log.Fields{logLayerName: name, "component count": len(allComponents)}).Debug("detected components")
	}

	var removedFiles []string
	for filePath := range files {
		base := filepath.Base(filePath)
		if base == whiteout.OpaqueDirectory {
			// The entire directory does not exist in lower layers.
			removedFiles = append(removedFiles, filepath.Dir(filePath))
		} else if strings.HasPrefix(base, whiteout.Prefix) {
			removed := base[len(whiteout.Prefix):]
			// Only prepend filepath.Dir if the directory is not `./`.
			if filePath != base {
				// We assume we only have Linux containers, so the path separator will be `/`.
				removed = fmt.Sprintf("%s/%s", filepath.Dir(filePath), removed)
			}
			removedFiles = append(removedFiles, removed)
		}
	}

	return namespace, distroless, featureVersions, rhelfeatures, allComponents, removedFiles, err
}

// DetectContentFromReader detects scanning content in the given reader.
func DetectContentFromReader(reader io.ReadCloser, format, name string, parent *database.Layer, uncertifiedRHEL bool) (*database.Namespace, bool, []database.FeatureVersion, *database.RHELv2Components, []*component.Component, []string, error) {
	files, err := imagefmt.ExtractFromReader(reader, format, requiredfilenames.SingletonMatcher())
	if err != nil {
		return nil, false, nil, nil, nil, nil, err
	}

	return detectFromFiles(files, name, parent, uncertifiedRHEL)
}

func isDistroless(filesMap tarutil.FilesMap) bool {
	_, ok := filesMap["var/lib/dpkg/status.d/"]
	return ok
}

// DetectNamespace detects the layer's namespace.
func DetectNamespace(name string, files tarutil.FilesMap, parent *database.Layer, uncertifiedRHEL bool) *database.Namespace {
	namespace := featurens.Detect(files, &featurens.DetectorOptions{
		UncertifiedRHEL: uncertifiedRHEL,
	})
	if namespace != nil {
		log.WithFields(log.Fields{logLayerName: name, "detected namespace": namespace.Name}).Debug("detected namespace")
		return namespace
	}

	// Fallback to the parent's namespace.
	if parent != nil {
		namespace = parent.Namespace
		if namespace != nil {
			log.WithFields(log.Fields{logLayerName: name, "detected namespace": namespace.Name}).Debug("detected namespace (from parent)")
			return namespace
		}
	}

	return nil
}

func detectFeatureVersions(name string, files tarutil.FilesMap, namespace *database.Namespace, parent *database.Layer) (features []database.FeatureVersion, err error) {
	// TODO(Quentin-M): We need to pass the parent image to DetectFeatures because it's possible that
	// some detectors would need it in order to produce the entire feature list (if they can only
	// detect a diff). Also, we should probably pass the detected namespace so detectors could
	// make their own decision.
	features, err = featurefmt.ListFeatures(files)
	if err != nil {
		return
	}

	// If there are no FeatureVersions, use parent's FeatureVersions if possible.
	// TODO(Quentin-M): We eventually want to give the choice to each detectors to use none/some of
	// their parent's FeatureVersions. It would be useful for detectors that can't find their entire
	// result using one Layer.
	if len(features) == 0 && parent != nil {
		features = parent.Features
		return
	}

	// Build a map of the namespaces for each FeatureVersion in our parent layer.
	parentFeatureNamespaces := make(map[string]database.Namespace)
	if parent != nil {
		for _, parentFeature := range parent.Features {
			parentFeatureNamespaces[parentFeature.Feature.Name+":"+parentFeature.Version] = parentFeature.Feature.Namespace
		}
	}

	// Ensure that each FeatureVersion has an associated Namespace.
	for i, feature := range features {
		if feature.Feature.Namespace.Name != "" {
			// There is a Namespace associated.
			continue
		}

		if parentFeatureNamespace, ok := parentFeatureNamespaces[feature.Feature.Name+":"+feature.Version]; ok {
			// The FeatureVersion is present in the parent layer; associate with their Namespace.
			features[i].Feature.Namespace = parentFeatureNamespace
			continue
		}

		if namespace != nil {
			// The Namespace has been detected in this layer; associate it.
			features[i].Feature.Namespace = *namespace
			continue
		}

		log.WithFields(log.Fields{"feature name": feature.Feature.Name, "feature version": feature.Version, logLayerName: name}).Warning("Namespace unknown")
		if featureFlags.ContinueUnknownOS.Enabled() {
			features = nil
			return
		}

		err = ErrUnsupported
		return
	}

	return
}
