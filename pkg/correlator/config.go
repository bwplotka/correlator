package correlator

import (
	"io/ioutil"

	"github.com/ghodss/yaml"
)

type Config struct {
	// Sources of Data
	Sources struct {
		Thanos struct {
			Source
		}
		Loki struct {
			Source
		}
		Jaeger struct {
			Source
		}
	}
}

type Source struct {
	Version          string
	InternalEndpoint string
	ExternalEndpoint string
}

func ParseConfigFromFile(cfgFile string) (Config, error) {
	c := Config{}

	b, err := ioutil.ReadFile(cfgFile)
	if err != nil {
		return c, err
	}

	return ParseConfig(b)
}

func ParseConfig(b []byte) (Config, error) {
	c := Config{}

	if err := yaml.Unmarshal(b, &c); err != nil {
		return c, err
	}
	return c, nil
}
