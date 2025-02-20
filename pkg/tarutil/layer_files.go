package tarutil

import (
	"fmt"
	"path"
	"strings"

	"github.com/stackrox/rox/pkg/set"
	"github.com/stackrox/scanner/pkg/elf"
	"github.com/stackrox/scanner/pkg/whiteout"
)

// FileData is the contents of a file and relevant metadata.
type FileData struct {
	// Contents is the contents of the file.
	Contents []byte
	// Executable indicates if the file is executable.
	Executable bool
	// ELFMetadata contains the dynamic library dependency metadata if the file is in ELF format.
	ELFMetadata *elf.Metadata
}

// LayerFiles represent the files in an image layer.
// It contains a map of the files' paths to their data and the information to resolve them.
type LayerFiles struct {
	data map[string]FileData
	// links maps a symbolic link to link target.
	links   map[string]string
	removed set.StringSet
}

// CreateNewLayerFiles creates a LayerFiles
func CreateNewLayerFiles(data map[string]FileData) LayerFiles {
	if data == nil {
		data = make(map[string]FileData)
	}
	return LayerFiles{data: data, links: make(map[string]string), removed: set.NewStringSet()}
}

// GetFilesMap returns the map of files to their data
func (f LayerFiles) GetFilesMap() map[string]FileData {
	return f.data
}

// Get resolves and gets FileData for the path
func (f LayerFiles) Get(path string) (FileData, bool) {
	resolved := f.resolve(path)
	if !strings.HasSuffix(resolved, "/") && strings.HasSuffix(path, "/") {
		resolved += "/"
	}
	fileData, exists := f.data[resolved]
	return fileData, exists
}

// MergeBaseAndResolveSymlinks merges a base LayerFiles to this and resolves all symbolic links
// The symbolic links are merged only for resolving paths and the files' data are not merged.
func (f LayerFiles) MergeBaseAndResolveSymlinks(base *LayerFiles) {
	if base != nil {
		for fileName, linkTo := range base.links {
			if f.removed.Contains(fileName) {
				continue
			}
			if _, exists := f.links[fileName]; exists {
				continue
			}
			f.links[fileName] = linkTo
		}
	}
	for fileName, linkTo := range f.links {
		f.links[fileName] = f.resolve(linkTo)
	}
}

// GetRemovedFiles returns the files removed
func (f LayerFiles) GetRemovedFiles() []string {
	return f.removed.AsSlice()
}

func (f LayerFiles) detectRemovedFiles() {
	for filePath := range f.data {
		base := path.Base(filePath)
		if base == whiteout.OpaqueDirectory {
			// The entire directory does not exist in lower layers.
			f.removed.Add(path.Dir(filePath))
		} else if strings.HasPrefix(base, whiteout.Prefix) {
			removed := base[len(whiteout.Prefix):]
			// Only prepend path.Dir if the directory is not `./`.
			if filePath != base {
				// We assume we only have Linux containers, so the path separator will be `/`.
				removed = fmt.Sprintf("%s/%s", path.Dir(filePath), removed)
			}
			f.removed.Add(removed)
		}
	}
}

// Resolve a path with symbolic links to its cleaned equivalent without
// symbolic links if it is resolvable.
// Eg. symlink -> file, and dirlink -> dir
// Resolve /dir/symlink to /dir/file and /dirlink/symlink to /dir/file
func (f LayerFiles) resolve(symLink string) string {
	resolved := symLink
	visited := set.NewStringSet()
	for curr, list := ".", strings.Split(symLink, "/"); len(list) > 0; {
		curr = path.Clean(curr + "/" + list[0])
		list = list[1:]

		if linkTo, ok := f.links[curr]; ok {
			if visited.Contains(curr) {
				// Detect a loop and return its current resolved path as best effort
				// like symlink1 <=> symlink2
				return resolved
			}
			visited.Add(curr)
			list = append(strings.Split(linkTo, "/"), list...)
			curr = "."
			resolved = strings.Join(list, "/")
		}
	}
	return resolved
}
