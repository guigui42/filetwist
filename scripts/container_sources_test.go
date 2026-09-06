package scripts

import (
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

func workflowText(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile("../.github/" + path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestReleasePublicationGuards(t *testing.T) {
	for path, guards := range map[string][]string{
		"workflows/release.yml": {
			"permissions:\n  contents: read",
			"  binaries:\n    if: github.event_name == 'push'",
			"  container:\n    if: github.event_name == 'push'",
			"upload-release-artifacts: ${{ github.event_name == 'workflow_dispatch' }}",
			"release-tag: ${{ github.event_name == 'push' && github.ref_name || '' }}",
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
			"ref: ${{ steps.release.outputs.sha }}",
		},
		"actions/prepare-container/action.yml": {
			"provenance: mode=max",
			"sbom: true",
			"push: false",
			"platforms: linux/amd64",
			"uses: ./.github/actions/setup-browser-tests",
			"record-validation",
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
		"needs: [validate, binaries]",
		"candidate-artifact: ${{ needs.validate.outputs.candidate-artifact }}",
		"candidate-revision: ${{ needs.validate.outputs.candidate-revision }}",
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
