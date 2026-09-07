package scripts

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestContainerSources(t *testing.T) {
	cmd := exec.CommandContext(t.Context(), "python3", "-B", "test_container_sources.py")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("source preparation regression tests: %v\n%s", err, output)
	}
}

func TestNextReleaseVersion(t *testing.T) {
	for _, test := range []struct {
		name string
		bump string
		tags string
		want string
	}{
		{name: "first patch", bump: "patch", want: "v0.0.1"},
		{name: "patch", bump: "patch", tags: "v0.0.1\nv0.0.3\n", want: "v0.0.4"},
		{name: "minor", bump: "minor", tags: "v1.2.9\n", want: "v1.3.0"},
		{name: "major", bump: "major", tags: "v1.9.9\n", want: "v2.0.0"},
		{name: "ignore non-stable tags", bump: "patch", tags: "v1.2.3\nv9.0.0-rc.1\nmedia-v4.0.0\n", want: "v1.2.4"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), "python3", "next-release-version.py", test.bump)
			cmd.Stdin = strings.NewReader(test.tags)
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("next version: %v\n%s", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != test.want {
				t.Fatalf("next version = %q; want %q", got, test.want)
			}
		})
	}
}

func workflowText(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile("../.github/" + path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestCodeQLProjectFilesExist(t *testing.T) {
	content, err := os.ReadFile("../.github/codeql-projects.json")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]struct {
		Projects map[string]struct {
			Files []string `json:"files"`
		} `json:"projects"`
	}
	if err := json.Unmarshal(content, &config); err != nil {
		t.Fatal(err)
	}
	for language, languageConfig := range config {
		for project, projectConfig := range languageConfig.Projects {
			for _, file := range projectConfig.Files {
				if strings.ContainsAny(file, "*?[") {
					continue
				}
				if _, err := os.Stat("../" + file); err != nil {
					t.Errorf("%s/%s CodeQL file %q is unavailable: %v", language, project, file, err)
				}
			}
		}
	}
}

func TestReleasePublicationGuards(t *testing.T) {
	for path, guards := range map[string][]string{
		"workflows/release.yml": {
			"permissions:\n  contents: read",
			"  workflow_call:",
			"  binaries:\n    if: needs.tag.outputs.publish == 'true'",
			"  container:\n    if: needs.tag.outputs.publish == 'true'",
			"upload-release-artifacts: ${{ needs.tag.outputs.publish != 'true' }}",
			"release-tag: ${{ needs.tag.outputs.release-tag }}",
		},
		"workflows/create-release.yml": {
			"permissions: {}",
			"      contents: write",
			"::error::Create releases from the default branch, not a tag",
			"test \"$GITHUB_REF_NAME\" = \"$DEFAULT_BRANCH\"",
			"git merge-base --is-ancestor \"$GITHUB_SHA\"",
			"python3 scripts/next-release-version.py \"$BUMP\"",
			"git tag -a \"$tag\"",
			"git push origin \"refs/tags/$tag\"",
			"uses: ./.github/workflows/release.yml",
			"release-tag: ${{ needs.tag.outputs.release-tag }}",
			"release-sha: ${{ needs.tag.outputs.release-sha }}",
		},
		"workflows/media-runtime.yml": {
			"permissions:\n  contents: read",
			"tags: ['media-v*']",
			"  runtime:\n    if: github.event_name == 'push'",
			"  container:\n    if: github.event_name == 'push'",
			"uses: ./.github/workflows/ci.yml",
			"uses: ./.github/workflows/container-publish.yml",
			"component: media",
			"candidate-artifact: ${{ needs.validate.outputs.candidate-artifact }}",
			"candidate-revision: ${{ needs.validate.outputs.candidate-revision }}",
			"      actions: read",
			"git merge-base --is-ancestor HEAD",
		},
		"workflows/container-publish.yml": {
			"  group: ghcr-${{ inputs.tag }}",
			"  cancel-in-progress: false",
			"    environment: ghcr",
			"    needs: [prepare, sources]",
			"artifact-ids: ${{ needs.prepare.outputs.candidate-artifact }}",
			"artifact-ids: ${{ needs.prepare.outputs.source-artifact }}",
			"DOCKER_CONFIG=\"$PWD/.release/anonymous-docker\" docker pull --platform linux/amd64",
			"vars.GHCR_PUBLISH_ENABLED",
			"vars.CONTAINER_SOURCE_BASE_URL",
			"git merge-base --is-ancestor \"$RELEASE_SHA\" \"refs/remotes/origin/$DEFAULT_BRANCH\"",
			"git worktree add --detach release-source \"$RELEASE_SHA\"",
			"recipe: ${{ steps.release.outputs.recipe }}",
			`--source-manifest ".release/assets/$SOURCE_MANIFEST"`,
		},
		"actions/prepare-container/action.yml": {
			"provenance: mode=max",
			"sbom: true",
			"push: false",
			"platforms: linux/amd64",
			"uses: ./.github/actions/setup-browser-tests",
			"record-validation",
			"deploy/Dockerfile|deploy/Dockerfile.runtime)",
			"file: ${{ inputs.source-directory }}/${{ inputs.recipe }}",
		},
	} {
		t.Run(path, func(t *testing.T) {
			content := workflowText(t, path)
			for _, guard := range guards {
				if !strings.Contains(content, guard) {
					t.Errorf("publication guard missing: %s", guard)
				}

			}
		})
	}
	content := workflowText(t, "workflows/container-publish.yml")
	_, publish, found := strings.Cut(content, "\n  publish:")
	if !found {
		t.Fatal("protected publication job missing")
	}
	for _, prohibited := range []string{"contents: write", "build-push-action", ":latest", "--clobber"} {
		if strings.Contains(publish, prohibited) {
			t.Errorf("protected image publication must not contain %q", prohibited)
		}
	}
}

func TestMediaLaneSharesChecksAndHasNoBinaryPublisher(t *testing.T) {
	media := workflowText(t, "workflows/media-runtime.yml")
	for _, duplicate := range []string{"goreleaser", "build-push-action@", "docker push", "scripts/validate-container.sh", "make test-browser", "--clobber", ":latest"} {
		if strings.Contains(media, duplicate) {
			t.Errorf("media release must reuse the common build and source/publish path: %s", duplicate)
		}
	}
	ci := workflowText(t, "workflows/ci.yml")
	if !strings.Contains(ci, "default: app") || !strings.Contains(ci, "recipe: ${{ steps.metadata.outputs.recipe }}") {
		t.Fatal("ordinary CI must select the application recipe through shared metadata")
	}
	if strings.Contains(workflowText(t, "workflows/release.yml"), "component: media") {
		t.Fatal("normal application releases must never select native compilation")
	}
	publish := workflowText(t, "workflows/container-publish.yml")
	prepare, tail, ok := strings.Cut(publish, "\n  sources:")
	if !ok {
		t.Fatal("source publication job missing")
	}
	sources, _, ok := strings.Cut(tail, "\n  publish:")
	if !ok || strings.Contains(prepare+sources, "packages: write") {
		t.Fatal("only the protected final image job may write packages")
	}
}

func TestReleaseReusesValidatedCICandidate(t *testing.T) {
	ci := workflowText(t, "workflows/ci.yml")
	publish := workflowText(t, "workflows/container-publish.yml")
	release := workflowText(t, "workflows/release.yml")
	action := workflowText(t, "actions/prepare-container/action.yml")
	for name, content := range map[string]string{"CI": ci, "image publication": publish} {
		if strings.Count(content, "uses: ./.github/actions/prepare-container") != 1 {
			t.Errorf("%s must use the shared container validation action exactly once", name)
		}
		for _, duplicate := range []string{"build-push-action@", "scripts/validate-container.sh", "scripts/measure-container.sh", "make test-browser"} {
			if strings.Contains(content, duplicate) {
				t.Errorf("%s duplicates the shared container action: %s", name, duplicate)
			}
		}
	}
	for _, link := range []string{
		"needs: [tag, validate, binaries]",
		"candidate-artifact: ${{ needs.validate.outputs.candidate-artifact }}",
		"candidate-revision: ${{ needs.validate.outputs.candidate-revision }}",
		"      actions: read",
	} {
		if !strings.Contains(release, link) {
			t.Errorf("release must pass validated CI outputs: %s", link)
		}
	}
	for _, guard := range []string{
		"test \"$CI_SHA\" = \"$RELEASE_SHA\"",
		"verify-validation",
		"if: inputs.candidate-artifact == ''\n        uses: ./.github/actions/prepare-container",
	} {
		if !strings.Contains(publish, guard) {
			t.Errorf("CI candidate reuse guard missing: %s", guard)
		}
	}
	receipt := strings.Index(action, "python3 scripts/container-image.py record-validation")
	upload := strings.Index(action, "id: candidate")
	for _, check := range []string{"run: scripts/validate-container.sh", "run: scripts/measure-container.sh", "run: make test-browser"} {
		position := strings.Index(action, check)
		if position < 0 || receipt < position || upload < receipt {
			t.Errorf("validation receipt/upload must follow successful %q", check)
		}
	}
}
