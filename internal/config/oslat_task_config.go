package config

import (
	"fmt"

	"gopkg.in/yaml.v3"
	corev1 "k8s.io/api/core/v1"
	k8syaml "sigs.k8s.io/yaml"
)

// Pod is a corev1.Pod that unmarshals from YAML using Kubernetes JSON tags
// (metadata, spec, …). yaml.v3 alone would look for Go field names.
type Pod struct {
	corev1.Pod `json:",inline"`
}

// UnmarshalYAML decodes a YAML mapping into a Kubernetes Pod.
func (p *Pod) UnmarshalYAML(value *yaml.Node) error {
	b, err := yaml.Marshal(value)
	if err != nil {
		return err
	}
	return k8syaml.Unmarshal(b, &p.Pod)
}

// OslatTaskConfig configures the oslat task.
// Exactly one of configFile or inline fields (timeout / pod) must be set.
type OslatTaskConfig struct {
	ConfigFile string   `yaml:"configFile,omitempty"`
	Timeout    Duration `yaml:"timeout,omitempty"`
	Pod        *Pod     `yaml:"pod,omitempty"`
	// MountKubeconfig, when true, auto-creates a Secret from the run's --kubeconfig
	// and injects it (volume + volumeMount + KUBECONFIG env) into every container,
	// so no manual `oc create secret` is needed.
	MountKubeconfig bool `yaml:"mountKubeconfig,omitempty"`
	// MountPath is where the kubeconfig is mounted in the pod (default /kubeconfig);
	// the file lands at <MountPath>/kubeconfig. Only used when MountKubeconfig is true.
	MountPath string `yaml:"mountPath,omitempty"`
}

func (c *OslatTaskConfig) hasInlineFields() bool {
	return c.Timeout.Duration != 0 || c.Pod != nil
}

func resolveAndValidateOslat(spec *TasksSpec) error {
	if spec.Oslat == nil {
		return nil
	}
	cfg := spec.Oslat
	if err := validateConfigFileXorInline(TaskConfigOslat, cfg.ConfigFile != "", cfg.hasInlineFields()); err != nil {
		return err
	}
	if cfg.ConfigFile != "" {
		loaded, err := loadOslatConfigFile(spec.ResolvePath(cfg.ConfigFile))
		if err != nil {
			return fmt.Errorf("%s: %w", TaskConfigOslat, err)
		}
		spec.Oslat = loaded
	}
	return validateOslatInline(spec.Oslat)
}

func loadOslatConfigFile(path string) (*OslatTaskConfig, error) {
	var wrap struct {
		Oslat *OslatTaskConfig `yaml:"oslat"`
	}
	if err := unmarshalWrappedConfig(path, &wrap); err != nil {
		return nil, err
	}
	if wrap.Oslat == nil {
		return nil, missingRootKeyError(path, TaskConfigOslat)
	}
	if wrap.Oslat.ConfigFile != "" {
		return nil, nestedConfigFileError(TaskConfigOslat)
	}
	return wrap.Oslat, nil
}

func validateOslatInline(cfg *OslatTaskConfig) error {
	if cfg.Timeout.Duration <= 0 {
		return fmt.Errorf("%s: timeout must be greater than 0", TaskConfigOslat)
	}
	if cfg.Pod == nil {
		return fmt.Errorf("%s: pod is required", TaskConfigOslat)
	}
	if cfg.Pod.Name == "" {
		return fmt.Errorf("%s: pod.metadata.name is required", TaskConfigOslat)
	}
	if len(cfg.Pod.Spec.Containers) == 0 {
		return fmt.Errorf("%s: pod.spec.containers must have at least one container", TaskConfigOslat)
	}
	for i, c := range cfg.Pod.Spec.Containers {
		if c.Image == "" {
			return fmt.Errorf("%s: pod.spec.containers[%d].image is required", TaskConfigOslat, i)
		}
	}
	return nil
}
