/*
   Copyright The Soci Snapshotter Authors.

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/awslabs/soci-snapshotter/soci"
	"github.com/awslabs/soci-snapshotter/util/dockershell"
	"github.com/containerd/containerd/platforms"
)

type testImageIndex struct {
	imgName         string
	platform        string
	imgInfo         imageInfo
	sociIndexDigest string
}

func prepareSociIndices(t *testing.T, sh *dockershell.Shell) []testImageIndex {
	testImages := []testImageIndex{
		{
			imgName:  "ubuntu:latest",
			platform: "linux/arm64",
		},
		{
			imgName:  "alpine:latest",
			platform: "linux/amd64",
		},
		{
			imgName:  "nginx:latest",
			platform: "linux/arm64",
		},
		{
			imgName:  "drupal:latest",
			platform: "linux/amd64",
		},
	}

	for i, img := range testImages {
		platform := platforms.DefaultSpec()
		if img.platform != "" {
			var err error
			platform, err = platforms.Parse(img.platform)
			if err != nil {
				t.Fatalf("could not parse platform: %v", err)
			}
		}
		img.imgInfo = dockerhub(img.imgName, withPlatform(platform))
		img.sociIndexDigest = optimizeImage(sh, img.imgInfo)
		testImages[i] = img
	}

	return testImages
}

func TestSociIndexInfo(t *testing.T) {
	t.Parallel()
	sh, done := newSnapshotterBaseShell(t)
	defer done()
	rebootContainerd(t, sh, "", "")

	testImages := prepareSociIndices(t, sh)

	for _, img := range testImages {
		t.Run(img.imgName, func(t *testing.T) {
			var sociIndex soci.Index
			rawJSON := sh.O("soci", "index", "info", img.sociIndexDigest)
			if err := json.Unmarshal(rawJSON, &sociIndex); err != nil {
				t.Fatalf("invalid soci index from digest %s: %v", img.sociIndexDigest, rawJSON)
			}

			m, err := getManifestDigest(sh, img.imgInfo.ref, img.imgInfo.platform)
			if err != nil {
				t.Fatalf("failed to get manifest digest: %v", err)
			}

			validateSociIndex(t, sh, sociIndex, m, nil)
		})
	}
}

func TestSociIndexList(t *testing.T) {
	t.Parallel()
	sh, done := newSnapshotterBaseShell(t)
	defer done()
	rebootContainerd(t, sh, "", "")

	testImages := prepareSociIndices(t, sh)

	existHandlerFull := func(output string, img testImageIndex) bool {
		// full output should have both img ref and soci index digest
		return strings.Contains(output, img.imgInfo.ref) && strings.Contains(output, img.sociIndexDigest)
	}

	existHandlerQuiet := func(output string, img testImageIndex) bool {
		// a given soci index should match exactly one line in the quiet output
		// for the first index, it should have prefix of digest+\n
		// for the rest, it should have `\n` before and after its digest
		return strings.HasPrefix(output, img.sociIndexDigest+"\n") || strings.Contains(output, "\n"+img.sociIndexDigest+"\n")
	}

	existHandlerExact := func(output string, img testImageIndex) bool {
		// when quiet output has only one index, it should be the exact soci_index_digest string
		return strings.Trim(output, "\n") == img.sociIndexDigest
	}

	// each test runs a soci command, filter to get expected images, and check
	// (only) expected images exist in command output
	tests := []struct {
		name         string
		command      []string
		filter       func(img testImageIndex) bool                // return true if `img` is expected in command output
		existHandler func(output string, img testImageIndex) bool // return true if `img` appears in `output`
	}{
		{
			name:         "`soci index ls` should list all soci indices",
			command:      []string{"soci", "index", "list"},
			filter:       func(img testImageIndex) bool { return true },
			existHandler: existHandlerFull,
		},
		{
			name:         "`soci index ls -q` should list digests of all soci indices",
			command:      []string{"soci", "index", "list", "-q"},
			filter:       func(img testImageIndex) bool { return true },
			existHandler: existHandlerQuiet,
		},
		{
			name:         "`soci index ls --ref imgRef` should only list soci indices for the image",
			command:      []string{"soci", "index", "list", "--ref", testImages[0].imgInfo.ref},
			filter:       func(img testImageIndex) bool { return img.imgInfo.ref == testImages[0].imgInfo.ref },
			existHandler: existHandlerFull,
		},
		{
			name:         "`soci index ls --platform linux/arm64` should only list soci indices for arm64 platform",
			command:      []string{"soci", "index", "list", "--platform", "linux/arm64"},
			filter:       func(img testImageIndex) bool { return img.platform == "linux/arm64" },
			existHandler: existHandlerFull,
		},
		{
			// make sure the image only generates one soci index (the test expects a single digest output)
			name:         "`soci index ls --ref imgRef -q` should print the exact soci index digest",
			command:      []string{"soci", "index", "list", "-q", "--ref", testImages[0].imgInfo.ref},
			filter:       func(img testImageIndex) bool { return img.imgInfo.ref == testImages[0].imgInfo.ref },
			existHandler: existHandlerExact,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			output := string(sh.O(tt.command...))
			for _, img := range testImages {
				expected := tt.filter(img)
				if expected && !tt.existHandler(output, img) {
					t.Fatalf("output doesn't have expected soci index: image: %s, soci index: %s", img.imgInfo.ref, img.sociIndexDigest)
				}
				if !expected && tt.existHandler(output, img) {
					t.Fatalf("output has unexpected soci index: image: %s, soci index: %s", img.imgInfo.ref, img.sociIndexDigest)
				}
			}
		})
	}
}
