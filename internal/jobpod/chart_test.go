package jobpod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// chartDir is the ard-serve chart, whose executor k8s objects (#319) encode
// this contract in NetworkPolicies and the admission policy.
var chartDir = filepath.Join("..", "..", "deploy", "charts", "ard-serve")

func readChart(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(chartDir, name))
	if err != nil {
		t.Fatalf("read chart file: %v", err)
	}
	return string(b)
}

// TestChartValuesMatchContract keeps the chart's jobs values equal to the
// constants the executor builds pods from.
func TestChartValuesMatchContract(t *testing.T) {
	var v struct {
		Jobs struct {
			ResultPort int `yaml:"resultPort"`
			AgentUID   int `yaml:"agentUID"`
			SidecarUID int `yaml:"sidecarUID"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal([]byte(readChart(t, "values.yaml")), &v); err != nil {
		t.Fatalf("parse values.yaml: %v", err)
	}
	for _, tc := range []struct {
		name      string
		got, want int
	}{
		{"jobs.resultPort", v.Jobs.ResultPort, ResultPort},
		{"jobs.agentUID", v.Jobs.AgentUID, AgentUID},
		{"jobs.sidecarUID", v.Jobs.SidecarUID, SidecarUID},
	} {
		if tc.got != tc.want {
			t.Errorf("values.yaml %s = %d, want %d (internal/jobpod)", tc.name, tc.got, tc.want)
		}
	}
}

// TestChartTemplatesMatchContract checks the names the templates hard-code.
func TestChartTemplatesMatchContract(t *testing.T) {
	quoted := make([]string, len(ModelEnv))
	for i, e := range ModelEnv {
		quoted[i] = "'" + e + "'"
	}
	for _, tc := range []struct {
		file, want string
	}{
		{"templates/jobs-admission.yaml", "[" + strings.Join(quoted, ", ") + "]"},
		{"templates/jobs-admission.yaml", "'" + LabelNetwork + "'"},
		{"templates/jobs-admission.yaml", "'" + LabelRepo + "'"},
		{"templates/jobs-admission.yaml", "'" + LabelJob + "'"},
		{"templates/jobs-admission.yaml", "variables.init[0].name == '" + ContainerPre + "'"},
		{"templates/jobs-admission.yaml", "c.name == '" + ContainerAgent + "'"},
		{"templates/jobs-admission.yaml", "c.name == '" + ContainerPost + "'"},
		{"templates/jobs-namespace.yaml", "{key: " + LabelNetwork + ", operator: NotIn"},
		{"templates/_helpers.tpl", LabelComponent + ": " + ComponentJob},
		{"templates/_helpers.tpl", LabelPartOf + ": ard"},
		{"templates/_helpers.tpl", LabelInstance + ": "},
	} {
		if !strings.Contains(readChart(t, tc.file), tc.want) {
			t.Errorf("%s does not contain %q", tc.file, tc.want)
		}
	}
}
