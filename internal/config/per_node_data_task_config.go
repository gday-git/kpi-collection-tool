package config

import "fmt"

// PerNodeDataTaskConfig configures the per-node data collection task.
// Exactly one of configFile or inline fields must be set.
type PerNodeDataTaskConfig struct {
	ConfigFile         string   `yaml:"configFile,omitempty"`
	Duration           Duration `yaml:"duration,omitempty"`
	Interval           Duration `yaml:"interval,omitempty"`
	Image              string   `yaml:"image,omitempty"`
	Isolcpus           string   `yaml:"isolcpus,omitempty"`
	WorkloadNamespaces []string `yaml:"workloadNamespaces,omitempty"`
	// EnhancedCollection additionally collects hardware inventory (CPU
	// model/family/cores, BIOS, DIMMs, disks) into hw_inventory-<node>.txt.
	EnhancedCollection bool `yaml:"enhanced-collection,omitempty"`
}

func (c *PerNodeDataTaskConfig) hasInlineFields() bool {
	return c.Duration.Duration != 0 ||
		c.Interval.Duration != 0 ||
		c.Image != "" ||
		c.Isolcpus != "" ||
		len(c.WorkloadNamespaces) > 0 ||
		c.EnhancedCollection
}

func resolveAndValidatePerNodeData(spec *TasksSpec) error {
	if spec.PerNodeData == nil {
		return nil
	}
	cfg := spec.PerNodeData
	if err := validateConfigFileXorInline(TaskConfigPerNodeData, cfg.ConfigFile != "", cfg.hasInlineFields()); err != nil {
		return err
	}
	if cfg.ConfigFile != "" {
		loaded, err := loadPerNodeDataConfigFile(spec.ResolvePath(cfg.ConfigFile))
		if err != nil {
			return fmt.Errorf("%s: %w", TaskConfigPerNodeData, err)
		}
		spec.PerNodeData = loaded
	}
	return validatePerNodeDataInline(spec.PerNodeData)
}

func loadPerNodeDataConfigFile(path string) (*PerNodeDataTaskConfig, error) {
	var wrap struct {
		PerNodeData *PerNodeDataTaskConfig `yaml:"per-node-data"`
	}
	if err := unmarshalWrappedConfig(path, &wrap); err != nil {
		return nil, err
	}
	if wrap.PerNodeData == nil {
		return nil, missingRootKeyError(path, TaskConfigPerNodeData)
	}
	if wrap.PerNodeData.ConfigFile != "" {
		return nil, nestedConfigFileError(TaskConfigPerNodeData)
	}
	return wrap.PerNodeData, nil
}

func validatePerNodeDataInline(cfg *PerNodeDataTaskConfig) error {
	if err := validatePollConfig(TaskConfigPerNodeData, cfg.WorkloadNamespaces, cfg.Interval, cfg.Duration); err != nil {
		return err
	}
	if cfg.Image == "" {
		return fmt.Errorf("%s: image is required", TaskConfigPerNodeData)
	}
	return nil
}
