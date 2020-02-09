package generatedump

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/stackrox/rox/pkg/concurrency"
	"github.com/stackrox/scanner/database"
	"github.com/stackrox/scanner/ext/vulnmdsrc/nvd"
	"github.com/stackrox/scanner/ext/vulnsrc"
	"github.com/stackrox/scanner/pkg/nvdloader"
	"github.com/stackrox/scanner/pkg/vulndump"
)

// An empty datastore makes all the updaters assume they're starting from scratch.
type emptyDataStore struct {
}

func (e emptyDataStore) GetKeyValue(key string) (string, error) {
	return "", nil
}

func generateDumpWithAllVulns(outFile string) error {
	if !strings.HasSuffix(outFile, ".zip") {
		return errors.Errorf("invalid outfile %q; must end in .zip", outFile)
	}

	// We will mark in the dump that the vulns are correct as of this time.
	// We cannot guarantee that any vuln is more up-to-date than this.
	startTime := time.Now()
	dumpDir, err := ioutil.TempDir("", "vuln-updater")
	if err != nil {
		return errors.Wrap(err, "failed to create temp dir")
	}
	log.Infof("Using temp dir %q for scratch space", dumpDir)
	defer func() {
		_ = os.RemoveAll(dumpDir)
	}()

	nvdSubDir := filepath.Join(dumpDir, vulndump.NVDDirName)
	if err := os.MkdirAll(nvdSubDir, 0755); err != nil {
		return errors.Wrap(err, "creating subdir for NVD")
	}

	log.Info("Downloading NVD...")
	if err := nvdloader.DownloadFeedsToPath(nvdSubDir); err != nil {
		return errors.Wrap(err, "downloading NVD")
	}

	log.Info("Fetching OS vulns...")
	fetchedVulns, err := fetchVulns(emptyDataStore{}, nvdSubDir)
	if err != nil {
		return errors.Wrap(err, "fetching vulns")
	}
	log.Infof("Finished fetching vulns (total: %d)", len(fetchedVulns))

	log.Info("Writing JSON file for updated vulns...")
	err = vulndump.WriteOSVulns(dumpDir, fetchedVulns)
	if err != nil {
		return err
	}

	log.Info("Writing manifest file...")
	err = vulndump.WriteManifestFile(dumpDir, vulndump.Manifest{
		Since: time.Time{}, // The zero time. Being explicit
		Until: startTime,
	})
	if err != nil {
		return err
	}
	log.Info("Zipping up the files...")
	if err := vulndump.WriteZip(dumpDir, outFile); err != nil {
		return errors.Wrap(err, "creating ZIP of the vuln dump")
	}
	log.Info("Done writing the zip with the entire vuln dump!")
	return nil
}

func Command() *cobra.Command {
	c := &cobra.Command{
		Use: "generate-dump",
	}
	outFile := c.Flags().String("out-file", "./dump.zip", "file to write the dump to")

	c.RunE = func(_ *cobra.Command, _ []string) error {
		return generateDumpWithAllVulns(*outFile)
	}

	return c
}

// fetch get data from the registered fetchers, in parallel.
func fetchVulns(datastore vulnsrc.DataStore, nvdDumpDir string) (vulns []database.Vulnerability, err error) {
	errSig := concurrency.NewErrorSignal()

	// Fetch updates in parallel.
	log.Info("fetching vulnerability updates")
	responseC := make(chan *vulnsrc.UpdateResponse)
	updaters := vulnsrc.Updaters()
	for n, u := range updaters {
		go func(name string, u vulnsrc.ExpectedCountAwareUpdater) {
			response, err := u.Update(datastore)
			if err != nil {
				log.WithError(err).WithField("updater name", name).Error("an error occurred when fetching update")
				errSig.SignalWithError(err)
				return
			}

			count := len(response.Vulnerabilities)
			if count < u.ExpectedCount() {
				err := errors.Errorf("expected %d, but obtained only %d vulnerabilities for updater %s", u.ExpectedCount(), count, name)
				log.Error(err)
				errSig.SignalWithError(err)
				return
			}
			select {
			case responseC <- &response:
				log.WithFields(map[string]interface{}{
					"updater name": name,
					"count":        count,
				}).Info("finished fetching")

			case <-errSig.Done():
				log.WithField("updater name", name).Warn("Exiting with error since another updater failed")
			}
		}(n, u)
	}

	// Collect results of updates.
	for i := 0; i < len(updaters); i++ {
		select {
		case resp := <-responseC:
			vulns = append(vulns, doVulnerabilitiesNamespacing(resp.Vulnerabilities)...)
			for _, note := range resp.Notes {
				log.WithField("note", note).Warn("There was a warning when running the updaters")
			}
		case <-errSig.Done():
			return nil, errSig.Err()
		}
	}

	for _, updater := range updaters {
		updater.Clean()
	}

	vulnsWithMetadata, err := addMetadata(vulns, nvdDumpDir)
	if err != nil {
		return nil, errors.Wrap(err, "adding metadata to vulns")
	}
	return vulnsWithMetadata, nil
}

// Add metadata to the specified vulnerabilities using the NVD metadata fetcher.
func addMetadata(vulnerabilities []database.Vulnerability, nvdDumpDir string) ([]database.Vulnerability, error) {
	log.Info("adding metadata to vulnerabilities")

	nvdAppender := nvd.SingletonAppender()
	if err := nvdAppender.BuildCache(nvdDumpDir); err != nil {
		return nil, errors.Wrap(err, "failed to build cache from the NVD feed dump")
	}
	defer nvdAppender.PurgeCache()
	for i := range vulnerabilities {
		vuln := &vulnerabilities[i]
		if err := nvdAppender.Append(vuln.Name, vuln.SubCVEs, appendFuncForVuln(vuln)); err != nil {
			return nil, errors.Wrapf(err, "Failed to append metadata for vuln %s", vuln.Name)
		}
	}

	return vulnerabilities, nil
}

func appendFuncForVuln(v *database.Vulnerability) nvd.AppendFunc {
	return func(metadataKey string, enricher nvd.MetadataEnricher, severity database.Severity) {
		// If necessary, initialize the metadata map for the vulnerability.
		if v.Metadata == nil {
			v.Metadata = make(map[string]interface{})
		}

		// Append the metadata.
		v.Metadata[metadataKey] = enricher.Metadata()
		if v.Description == "" {
			v.Description = enricher.Summary()
		}

		// If necessary, provide a severity for the vulnerability.
		if v.Severity == database.UnknownSeverity {
			v.Severity = severity
		}
	}
}

// doVulnerabilitiesNamespacing takes Vulnerabilities that don't have a
// Namespace and split them into multiple vulnerabilities that have a Namespace
// and only contains the FixedIn FeatureVersions corresponding to their
// Namespace.
//
// It helps simplifying the fetchers that share the same metadata about a
// Vulnerability regardless of their actual namespace (ie. same vulnerability
// information for every version of a distro).
func doVulnerabilitiesNamespacing(nonNamespacedVulns []database.Vulnerability) []database.Vulnerability {
	namespacedVulnsMap := make(map[string]*database.Vulnerability)

	for _, nonNamespacedVuln := range nonNamespacedVulns {
		featureVersions := nonNamespacedVuln.FixedIn
		nonNamespacedVuln.FixedIn = []database.FeatureVersion{}

		for _, fv := range featureVersions {
			index := fv.Feature.Namespace.Name + ":" + nonNamespacedVuln.Name

			namespacedVuln := namespacedVulnsMap[index]
			if namespacedVuln == nil {
				newVuln := nonNamespacedVuln
				newVuln.Namespace = fv.Feature.Namespace
				namespacedVuln = &newVuln
				namespacedVulnsMap[index] = namespacedVuln
			}
			namespacedVuln.FixedIn = append(namespacedVuln.FixedIn, fv)
		}
	}

	// Convert map into a slice.
	response := make([]database.Vulnerability, 0, len(namespacedVulnsMap))
	for _, vulnerability := range namespacedVulnsMap {
		response = append(response, *vulnerability)
	}

	return response
}
