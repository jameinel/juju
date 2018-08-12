// Copyright 2018 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package provider_test

import (
	"github.com/juju/testing"
	jc "github.com/juju/testing/checkers"
	"github.com/juju/utils"
	gc "gopkg.in/check.v1"
	core "k8s.io/api/core/v1"
	apiextensionsv1beta1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1beta1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/juju/juju/caas"
	"github.com/juju/juju/caas/kubernetes/provider"
	"github.com/juju/juju/cloud"
	"github.com/juju/juju/environs"
	"github.com/juju/juju/environs/config"
	coretesting "github.com/juju/juju/testing"
)

func fakeConfig(c *gc.C, attrs ...coretesting.Attrs) *config.Config {
	cfg, err := coretesting.ModelConfig(c).Apply(fakeConfigAttrs(attrs...))
	c.Assert(err, jc.ErrorIsNil)
	return cfg
}

func fakeConfigAttrs(attrs ...coretesting.Attrs) coretesting.Attrs {
	merged := coretesting.FakeConfig().Merge(coretesting.Attrs{
		"type": "kubernetes",
		"uuid": utils.MustNewUUID().String(),
	})
	for _, attrs := range attrs {
		merged = merged.Merge(attrs)
	}
	return merged
}

func fakeCloudSpec() environs.CloudSpec {
	cred := fakeCredential()
	return environs.CloudSpec{
		Type:       "kubernetes",
		Name:       "k8s",
		Endpoint:   "host1",
		Credential: &cred,
	}
}

func fakeCredential() cloud.Credential {
	return cloud.NewCredential(cloud.UserPassAuthType, map[string]string{
		"username": "user1",
		"password": "password1",
	})
}

type providerSuite struct {
	testing.IsolationSuite
	dialStub testing.Stub
	provider caas.ContainerEnvironProvider
}

var _ = gc.Suite(&providerSuite{})

func (s *providerSuite) SetUpTest(c *gc.C) {
	s.IsolationSuite.SetUpTest(c)
	s.dialStub.ResetCalls()
	s.provider = provider.NewProvider()
}

func (s *providerSuite) TestRegistered(c *gc.C) {
	provider, err := environs.Provider("kubernetes")
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(provider, gc.NotNil)
}

func (s *providerSuite) TestOpen(c *gc.C) {
	config := fakeConfig(c)
	broker, err := s.provider.Open(environs.OpenParams{
		Cloud:  fakeCloudSpec(),
		Config: config,
	})
	c.Check(err, jc.ErrorIsNil)
	c.Assert(broker, gc.NotNil)
}

func (s *providerSuite) TestOpenInvalidCloudSpec(c *gc.C) {
	spec := fakeCloudSpec()
	spec.Name = ""
	s.testOpenError(c, spec, `validating cloud spec: cloud name "" not valid`)
}

func (s *providerSuite) TestOpenMissingCredential(c *gc.C) {
	spec := fakeCloudSpec()
	spec.Credential = nil
	s.testOpenError(c, spec, `validating cloud spec: missing credential not valid`)
}

func (s *providerSuite) TestOpenUnsupportedCredential(c *gc.C) {
	credential := cloud.NewCredential(cloud.OAuth1AuthType, map[string]string{})
	spec := fakeCloudSpec()
	spec.Credential = &credential
	s.testOpenError(c, spec, `validating cloud spec: "oauth1" auth-type not supported`)
}

func (s *providerSuite) testOpenError(c *gc.C, spec environs.CloudSpec, expect string) {
	_, err := s.provider.Open(environs.OpenParams{
		Cloud:  spec,
		Config: fakeConfig(c),
	})
	c.Assert(err, gc.ErrorMatches, expect)
}

func (s *providerSuite) TestPrepareConfig(c *gc.C) {
	cfg, err := s.provider.PrepareConfig(environs.PrepareConfigParams{
		Config: fakeConfig(c),
		Cloud:  fakeCloudSpec(),
	})
	c.Check(err, jc.ErrorIsNil)
	c.Check(cfg, gc.NotNil)
}

func (s *providerSuite) TestValidate(c *gc.C) {
	config := fakeConfig(c)
	validCfg, err := s.provider.Validate(config, nil)
	c.Check(err, jc.ErrorIsNil)

	validAttrs := validCfg.AllAttrs()
	c.Assert(config.AllAttrs(), gc.DeepEquals, validAttrs)
}

func (s *providerSuite) TestParsePodSpec(c *gc.C) {

	specStr := `
omitServiceFrontend: true
containers:
  - name: gitlab
    image: gitlab/latest
    imagePullPolicy: Always
    ports:
    - containerPort: 80
      protocol: TCP
    - containerPort: 443
    livenessProbe:
      initialDelaySeconds: 10
      httpget:
        path: /ping
        port: 8080
    readinessProbe:
      initialDelaySeconds: 10
      httpget:
        path: /pingReady
        port: www
    config:
      attr: foo=bar; fred=blogs
      foo: bar
    files:
      - name: configuration
        mountPath: /var/lib/foo
        files:
          file1: |
            [config]
            foo: bar
crd:
  group: kubeflow.org
  version: v1alpha2
  scope: Namespaced
  kind: TFJob
  validation:
    properties:
      tfReplicaSpecs:
        properties:
          Worker:
            properties:
              replicas:
                type: integer
                minimum: 1
          PS:
            properties:
              replicas:
                type: integer
                minimum: 1
          Chief:
            properties:
              replicas:
                type: integer
                minimum: 1
                maximum: 1
`[1:]

	expectedFileContent := `
[config]
foo: bar
`[1:]

	k8sprovider := provider.NewProvider()
	spec, err := k8sprovider.ParsePodSpec(specStr)
	c.Assert(err, jc.ErrorIsNil)
	c.Assert(spec, jc.DeepEquals, &caas.PodSpec{
		OmitServiceFrontend: true,
		Containers: []caas.ContainerSpec{{
			Name:  "gitlab",
			Image: "gitlab/latest",
			Ports: []caas.ContainerPort{
				{ContainerPort: 80, Protocol: "TCP"},
				{ContainerPort: 443},
			},
			ProviderContainer: &provider.K8sContainerSpec{
				ImagePullPolicy: "Always",
				LivenessProbe: &core.Probe{
					InitialDelaySeconds: 10,
					Handler: core.Handler{
						HTTPGet: &core.HTTPGetAction{
							Path: "/ping",
							Port: intstr.IntOrString{IntVal: 8080},
						},
					},
				},
				ReadinessProbe: &core.Probe{
					InitialDelaySeconds: 10,
					Handler: core.Handler{
						HTTPGet: &core.HTTPGetAction{
							Path: "/pingReady",
							Port: intstr.IntOrString{StrVal: "www", Type: 1},
						},
					},
				}},
			Config: map[string]string{
				"attr": "foo=bar; fred=blogs",
				"foo":  "bar",
			},
			Files: []caas.FileSet{
				{
					Name:      "configuration",
					MountPath: "/var/lib/foo",
					Files: map[string]string{
						"file1": expectedFileContent,
					},
				},
			},
		}},
		CustomResourceDefinition: caas.CustomResourceDefinition{
			Kind:    "TFJob",
			Group:   "kubeflow.org",
			Version: "v1alpha2",
			Scope:   "Namespaced",
			Validation: caas.CrdTemplateValidation{
				Properties: map[string]apiextensionsv1beta1.JSONSchemaProps{
					"tfReplicaSpecs": {
						Properties: map[string]apiextensionsv1beta1.JSONSchemaProps{
							"PS": {
								Properties: map[string]apiextensionsv1beta1.JSONSchemaProps{
									"replicas": {
										Type: "integer", Minimum: float64Ptr(1),
									},
								},
							},
							"Chief": {
								Properties: map[string]apiextensionsv1beta1.JSONSchemaProps{
									"replicas": {
										Type:    "integer",
										Minimum: float64Ptr(1),
										Maximum: float64Ptr(1),
									},
								},
							},
							"Worker": {
								Properties: map[string]apiextensionsv1beta1.JSONSchemaProps{
									"replicas": {
										Type:    "integer",
										Minimum: float64Ptr(1),
									},
								},
							},
						},
					},
				},
			},
		},
	})
}

func float64Ptr(f float64) *float64 {
	return &f
}

func (s *providerSuite) TestValidateMissingContainers(c *gc.C) {

	specStr := `
containers:
`[1:]

	_, err := provider.ParseK8sPodSpec(specStr)
	c.Assert(err, gc.ErrorMatches, "require at least one container spec")
}

func (s *providerSuite) TestValidateMissingName(c *gc.C) {

	specStr := `
containers:
  - image: gitlab/latest
`[1:]

	spec, err := provider.ParseK8sPodSpec(specStr)
	c.Assert(err, jc.ErrorIsNil)
	err = spec.Validate()
	c.Assert(err, gc.ErrorMatches, "spec name is missing")
}

func (s *providerSuite) TestValidateMissingImage(c *gc.C) {

	specStr := `
containers:
  - name: gitlab
`[1:]

	spec, err := provider.ParseK8sPodSpec(specStr)
	c.Assert(err, jc.ErrorIsNil)
	err = spec.Validate()
	c.Assert(err, gc.ErrorMatches, "spec image details is missing")
}

func (s *providerSuite) TestValidateFileSetPath(c *gc.C) {

	specStr := `
containers:
  - name: gitlab
    image: gitlab/latest
    files:
      - files:
          file1: |-
            [config]
            foo: bar
`[1:]

	spec, err := provider.ParseK8sPodSpec(specStr)
	c.Assert(err, jc.ErrorIsNil)
	err = spec.Validate()
	c.Assert(err, gc.ErrorMatches, `file set name is missing`)
}

func (s *providerSuite) TestValidateMissingMountPath(c *gc.C) {

	specStr := `
containers:
  - name: gitlab
    image: gitlab/latest
    files:
      - name: configuration
        files:
          file1: |-
            [config]
            foo: bar
`[1:]

	spec, err := provider.ParseK8sPodSpec(specStr)
	c.Assert(err, jc.ErrorIsNil)
	err = spec.Validate()
	c.Assert(err, gc.ErrorMatches, `mount path is missing for file set "configuration"`)
}
