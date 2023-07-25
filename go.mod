module github.com/lwolf/kube-cleanup-operator

go 1.19

require (
	github.com/VictoriaMetrics/metrics v1.11.3
	github.com/cenkalti/backoff/v4 v4.1.3
	github.com/projectcalico/calico v3.21.1+incompatible
	k8s.io/api v0.27.0
	k8s.io/apimachinery v0.27.0
	k8s.io/client-go v0.27.0
	k8s.io/klog v1.0.0
	k8s.io/klog/v2 v2.90.1
	k8s.io/kubernetes v1.26.3
	k8s.io/utils v0.0.0-20230313181309-38a27ef9d749
)

require (
	github.com/coreos/go-semver v0.3.0 // indirect
	github.com/coreos/go-systemd/v22 v22.3.2 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/emicklei/go-restful/v3 v3.9.0 // indirect
	github.com/go-logr/logr v1.2.3 // indirect
	github.com/go-openapi/jsonpointer v0.19.6 // indirect
	github.com/go-openapi/jsonreference v0.20.1 // indirect
	github.com/go-openapi/swag v0.22.3 // indirect
	github.com/go-playground/locales v0.12.1 // indirect
	github.com/go-playground/universal-translator v0.0.0-20170327191703-71201497bace // indirect
	github.com/gogo/protobuf v1.3.2 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/golang/protobuf v1.5.3 // indirect
	github.com/google/gnostic v0.5.7-v3refs // indirect
	github.com/google/go-cmp v0.5.9 // indirect
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/google/uuid v1.3.0 // indirect
	github.com/imdario/mergo v0.3.8 // indirect
	github.com/josharian/intern v1.0.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kelseyhightower/envconfig v0.0.0-20180517194557-dd1402a4d99d // indirect
	github.com/leodido/go-urn v0.0.0-20181204092800-a67a23e1c1af // indirect
	github.com/mailru/easyjson v0.7.7 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/projectcalico/api v0.0.0-00010101000000-000000000000 // indirect
	github.com/projectcalico/go-json v0.0.0-20161128004156-6219dc7339ba // indirect
	github.com/projectcalico/go-yaml-wrapper v0.0.0-20191112210931-090425220c54 // indirect
	github.com/sirupsen/logrus v1.9.0 // indirect
	github.com/spf13/pflag v1.0.5 // indirect
	github.com/valyala/fastrand v1.0.0 // indirect
	github.com/valyala/histogram v1.0.1 // indirect
	go.etcd.io/etcd/api/v3 v3.5.8 // indirect
	go.etcd.io/etcd/client/pkg/v3 v3.5.8 // indirect
	go.etcd.io/etcd/client/v3 v3.5.8 // indirect
	go.uber.org/atomic v1.9.0 // indirect
	go.uber.org/multierr v1.8.0 // indirect
	go.uber.org/zap v1.21.0 // indirect
	golang.org/x/crypto v0.1.0 // indirect
	golang.org/x/net v0.8.0 // indirect
	golang.org/x/oauth2 v0.0.0-20221014153046-6fdb5e3db783 // indirect
	golang.org/x/sync v0.1.0 // indirect
	golang.org/x/sys v0.6.0 // indirect
	golang.org/x/term v0.6.0 // indirect
	golang.org/x/text v0.8.0 // indirect
	golang.org/x/time v0.1.0 // indirect
	golang.zx2c4.com/wireguard/wgctrl v0.0.0-20200324154536-ceff61240acf // indirect
	google.golang.org/appengine v1.6.7 // indirect
	google.golang.org/genproto v0.0.0-20221227171554-f9683d7f8bef // indirect
	google.golang.org/grpc v1.52.0 // indirect
	google.golang.org/protobuf v1.28.1 // indirect
	gopkg.in/go-playground/validator.v9 v9.27.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	k8s.io/kube-openapi v0.0.0-20230308215209-15aac26d736a // indirect
	sigs.k8s.io/json v0.0.0-20221116044647-bc3834ca7abd // indirect
	sigs.k8s.io/structured-merge-diff/v4 v4.2.3 // indirect
	sigs.k8s.io/yaml v1.3.0 // indirect
)

replace (
	github.com/projectcalico/api => github.com/projectcalico/api v0.0.0-20230602153125-fb7148692637 // v3.26.0
	github.com/projectcalico/calico => github.com/projectcalico/calico v0.0.0-20230526220659-8b103f46fbdc
	k8s.io/api => k8s.io/api v0.27.0
	k8s.io/apiextensions-apiserver => k8s.io/apiextensions-apiserver v0.27.0
	k8s.io/apimachinery => k8s.io/apimachinery v0.27.0
	k8s.io/apiserver => k8s.io/apiserver v0.27.0
	k8s.io/cli-runtime => k8s.io/cli-runtime v0.27.0
	k8s.io/client-go => k8s.io/client-go v0.27.0
	k8s.io/cloud-provider => k8s.io/cloud-provider v0.27.0
	k8s.io/cluster-bootstrap => k8s.io/cluster-bootstrap v0.27.0
	k8s.io/code-generator => k8s.io/code-generator v0.27.0
	k8s.io/component-base => k8s.io/component-base v0.27.0
	k8s.io/component-helpers => k8s.io/component-helpers v0.27.0
	k8s.io/controller-manager => k8s.io/controller-manager v0.27.0
	k8s.io/cri-api => k8s.io/cri-api v0.27.0
	k8s.io/csi-translation-lib => k8s.io/csi-translation-lib v0.27.0
	k8s.io/dynamic-resource-allocation => k8s.io/dynamic-resource-allocation v0.27.0
	k8s.io/kms => k8s.io/kms v0.27.0
	k8s.io/kube-aggregator => k8s.io/kube-aggregator v0.27.0
	k8s.io/kube-controller-manager => k8s.io/kube-controller-manager v0.27.0
	k8s.io/kube-proxy => k8s.io/kube-proxy v0.27.0
	k8s.io/kube-scheduler => k8s.io/kube-scheduler v0.27.0
	k8s.io/kubectl => k8s.io/kubectl v0.27.0
	k8s.io/kubelet => k8s.io/kubelet v0.27.0
	k8s.io/legacy-cloud-providers => k8s.io/legacy-cloud-providers v0.27.0
	k8s.io/metrics => k8s.io/metrics v0.27.0
	k8s.io/mount-utils => k8s.io/mount-utils v0.27.0
	k8s.io/pod-security-admission => k8s.io/pod-security-admission v0.27.0
	k8s.io/sample-apiserver => k8s.io/sample-apiserver v0.27.0
)
