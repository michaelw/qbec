/*
   Copyright 2019 Splunk Inc.

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

package remote

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/googleapis/gnostic/OpenAPIv2"
	"github.com/pkg/errors"
	"github.com/splunk/qbec/internal/model"
	"github.com/splunk/qbec/internal/sio"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/util/proto"
	"k8s.io/kube-openapi/pkg/util/proto/validation"
	"k8s.io/kubernetes/pkg/kubectl/cmd/util/openapi"
)

// Validator validates documents of a specific type.
type Validator interface {
	// Validate validates the supplied object and returns a slice of validation errors.
	Validate(obj *unstructured.Unstructured) []error
}

// vsSchema implements Validator
type vsSchema struct {
	proto.Schema
}

func (v *vsSchema) Validate(obj *unstructured.Unstructured) []error {
	gvk := obj.GroupVersionKind()
	return validation.ValidateModel(obj.UnstructuredContent(), v.Schema, fmt.Sprintf("%s.%s", gvk.Version, gvk.Kind))
}

type schemaResult struct {
	validator Validator
	err       error
}

// validators produces Validator instances for k8s types.
type validators struct {
	res   openapi.Resources
	l     sync.Mutex
	cache map[schema.GroupVersionKind]*schemaResult
}

func (v *validators) validatorFor(gvk schema.GroupVersionKind) (Validator, error) {
	v.l.Lock()
	defer v.l.Unlock()
	sr := v.cache[gvk]
	if sr == nil {
		var err error
		valSchema := v.res.LookupResource(gvk)
		if valSchema == nil {
			err = ErrSchemaNotFound
		}
		sr = &schemaResult{
			validator: &vsSchema{valSchema},
			err:       err,
		}
		v.cache[gvk] = sr
	}
	return sr.validator, sr.err
}

// openapiResourceResult is the cached result of retrieving an openAPI doc from the server.
type openapiResourceResult struct {
	res        openapi.Resources
	validators *validators
	err        error
}

// gvkInfo is all the information we need for k8s types as represented by group-version-kind.
type gvkInfo struct {
	canonical schema.GroupVersionKind // the preferred gvk that includes aliasing (e.g. extensions/v1beta1 => apps/v1)
	resource  metav1.APIResource      // the API resource for the gvk
}

type minimalDiscovery interface {
	ServerGroups() (*metav1.APIGroupList, error)
	ServerResourcesForGroupVersion(groupVersion string) (*metav1.APIResourceList, error)
	OpenAPISchema() (*openapi_v2.Document, error)
}

// ServerMetadata provides metadata information for a K8s cluster.
type ServerMetadata struct {
	disco     minimalDiscovery
	registry  map[schema.GroupVersionKind]*gvkInfo
	defaultNs string
	ol        sync.Mutex
	oResult   *openapiResourceResult
	verbosity int
}

func newServerMetadata(disco minimalDiscovery, defaultNs string, verbosity int) (*ServerMetadata, error) {
	sm := &ServerMetadata{
		disco:     disco,
		registry:  map[schema.GroupVersionKind]*gvkInfo{},
		defaultNs: defaultNs,
		verbosity: verbosity,
	}
	if err := sm.init(); err != nil {
		return nil, err
	}
	return sm, nil
}

func (sm *ServerMetadata) infoFor(gvk schema.GroupVersionKind) (*gvkInfo, error) {
	res, ok := sm.registry[gvk]
	if !ok {
		return nil, fmt.Errorf("server does not recognize gvk %s", gvk)
	}
	return res, nil
}

// ValidatorFor returns a validator for the supplied GroupVersionKind.
func (sm *ServerMetadata) ValidatorFor(gvk schema.GroupVersionKind) (Validator, error) {
	_, v, err := sm.openAPIResources()
	if err != nil {
		return nil, err
	}
	return v.validatorFor(gvk)
}

// DisplayName returns a display name for the supplied object in a format that mimics
// phrases that can be pasted into kubectl commands.
func (sm *ServerMetadata) DisplayName(o model.K8sMeta) string {
	gvk := o.GetObjectKind().GroupVersionKind()
	info := sm.registry[gvk]

	displayType := func() string {
		if info != nil {
			return info.resource.Name
		}
		return strings.ToLower(gvk.Kind)
	}

	displayName := func() string {
		ns := o.GetNamespace()
		name := o.GetName()
		if info != nil {
			if info.resource.Namespaced {
				if ns == "" {
					ns = sm.defaultNs
				}
			} else {
				ns = ""
			}
		}
		if ns == "" {
			return name
		}
		return name + " -n " + ns
	}
	name := fmt.Sprintf("%s %s", displayType(), displayName())
	if l, ok := o.(model.K8sLocalObject); ok {
		comp := l.Component()
		if comp != "" {
			name += fmt.Sprintf(" (source %s)", comp)
		}
	}
	return name
}

// IsNamespaced returns true if the resource corresponding to the supplied
// GroupVersionKind is namespaced.
func (sm *ServerMetadata) IsNamespaced(gvk schema.GroupVersionKind) (bool, error) {
	info, err := sm.infoFor(gvk)
	if err != nil {
		return false, err
	}
	return info.resource.Namespaced, nil
}

func (sm *ServerMetadata) collectTypes(filter func(*gvkInfo) bool) []schema.GroupVersionKind {
	canonicalTypes := map[schema.GroupVersionKind]bool{}
	for _, t := range sm.registry {
		canonicalTypes[t.canonical] = true
	}
	var ret []schema.GroupVersionKind
	for t := range canonicalTypes {
		info := sm.registry[t]
		if info == nil {
			panic(fmt.Errorf("no info for %s", t))
		}
		if filter(info) {
			ret = append(ret, t)
		}
	}
	return ret
}

func (sm *ServerMetadata) namespacedTypes() []schema.GroupVersionKind {
	return sm.collectTypes(func(info *gvkInfo) bool { return info.resource.Namespaced })
}

func (sm *ServerMetadata) clusterTypes() []schema.GroupVersionKind {
	return sm.collectTypes(func(info *gvkInfo) bool { return !info.resource.Namespaced })
}

// canonicalGroupVersionKind provides the preferred/ canonical group version kind for the supplied input.
// It takes aliases into account (e.g. extensions/Deployment same as apps/Deployment) for doing so.
func (sm *ServerMetadata) canonicalGroupVersionKind(gvk schema.GroupVersionKind) (schema.GroupVersionKind, error) {
	info, err := sm.infoFor(gvk)
	if err != nil {
		return gvk, err
	}
	return info.canonical, nil
}

type equivalence struct {
	gk1 schema.GroupKind
	gk2 schema.GroupKind
}

// equivalences from https://github.com/kubernetes/kubernetes/blob/master/pkg/kubeapiserver/default_storage_factory_builder.go
var equivalences = []equivalence{
	{
		gk1: schema.GroupKind{Group: "networking.k8s.io", Kind: "NetworkPolicy"},
		gk2: schema.GroupKind{Group: "extensions", Kind: "NetworkPolicy"},
	},
	{
		gk1: schema.GroupKind{Group: "apps", Kind: "Deployment"},
		gk2: schema.GroupKind{Group: "extensions", Kind: "Deployment"},
	},
	{
		gk1: schema.GroupKind{Group: "apps", Kind: "DaemonSet"},
		gk2: schema.GroupKind{Group: "extensions", Kind: "DaemonSet"},
	},
	{
		gk1: schema.GroupKind{Group: "", Kind: "Event"},
		gk2: schema.GroupKind{Group: "events.k8s.io", Kind: "Event"},
	},
	{
		gk1: schema.GroupKind{Group: "policy", Kind: "PodSecurityPolicy"},
		gk2: schema.GroupKind{Group: "extensions", Kind: "PodSecurityPolicy"},
	},
}

func eligibleResource(r metav1.APIResource) bool {
	needed := []string{"create", "delete", "get", "list"}
	for _, n := range needed {
		found := false
		for _, v := range r.Verbs {
			if n == v {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

type resolver struct {
	group            string
	version          string
	groupVersion     string
	preferredVersion string
	registry         map[schema.GroupVersionKind]*gvkInfo
	tracker          map[schema.GroupKind][]schema.GroupVersionKind
	err              error
}

func (r *resolver) resolve(disco minimalDiscovery) {
	reg := map[schema.GroupVersionKind]*gvkInfo{}
	tracker := map[schema.GroupKind][]schema.GroupVersionKind{}
	list, err := disco.ServerResourcesForGroupVersion(r.groupVersion)
	if err != nil {
		sio.Warnln("error getting resources for type", r.groupVersion, ":", err)
	}
	if list != nil {
		for _, res := range list.APIResources {
			if strings.Contains(res.Name, "/") { // ignore sub-resources
				continue
			}
			if !eligibleResource(res) { // remove stuff we cannot create and delete
				continue
			}
			kindName := res.Kind
			gvk := schema.GroupVersionKind{Group: r.group, Version: r.version, Kind: kindName}
			// the canonical version of the type may not be correct at this stage if the preferred group version
			// does not have the specific kind. We will fix these anomalies later when all objects have been loaded
			// and are known.
			reg[gvk] = &gvkInfo{
				canonical: schema.GroupVersionKind{Group: r.group, Version: r.preferredVersion, Kind: kindName},
				resource:  res,
			}
			gk := schema.GroupKind{Group: r.group, Kind: kindName}
			tracker[gk] = append(tracker[gk], gvk)
		}
	}
	r.registry = reg
	r.tracker = tracker
}

func (sm *ServerMetadata) init() error {
	start := time.Now()
	groups, err := sm.disco.ServerGroups()
	if err != nil {
		return errors.Wrap(err, "get server groups")
	}

	order := 0
	groupOrderMap := map[string]int{}

	var resolvers []*resolver
	for _, group := range groups.Groups {
		groupName := group.Name
		order++
		groupOrderMap[groupName] = order
		preferredVersionName := group.PreferredVersion.Version
		for _, gv := range group.Versions {
			versionName := gv.Version
			resolvers = append(resolvers, &resolver{
				group:            groupName,
				version:          versionName,
				preferredVersion: preferredVersionName,
				groupVersion:     gv.GroupVersion,
			})
		}
	}

	var wg sync.WaitGroup
	wg.Add(len(resolvers))
	for _, r := range resolvers {
		go func(resolver *resolver) {
			defer wg.Done()
			resolver.resolve(sm.disco)
		}(r)
	}
	wg.Wait()

	reg := map[schema.GroupVersionKind]*gvkInfo{}
	// tracker tracks all known versions for a given group kind for the purposes of updating
	// the canonical versions for equivalences.
	tracker := map[schema.GroupKind][]schema.GroupVersionKind{}
	for _, r := range resolvers {
		if r.err != nil {
			return r.err
		}
		for k, v := range r.registry {
			reg[k] = v
		}
		for k, v := range r.tracker {
			tracker[k] = append(tracker[k], v...)
		}
	}

	// now deal with incorrect preferred versions when specific types do not exist for those
	var fixTypes []schema.GroupVersionKind // collect list of types to be fixed
	for k, v := range reg {
		canon := v.canonical
		if reg[canon] == nil {
			fixTypes = append(fixTypes, k)
		}
	}
	for _, k := range fixTypes {
		v := reg[k]
		reg[k] = &gvkInfo{
			canonical: k,
			resource:  v.resource,
		}
	}

	// then process aliases
	for _, eq := range equivalences {
		gk1 := eq.gk1
		gk2 := eq.gk2
		_, gk1Present := tracker[gk1]
		_, gk2Present := tracker[gk2]
		if !(gk1Present && gk2Present) {
			continue
		}
		g1Order := groupOrderMap[gk1.Group]
		g2Order := groupOrderMap[gk2.Group]
		var canonicalGK, aliasGK schema.GroupKind
		if g1Order < g2Order {
			canonicalGK, aliasGK = eq.gk1, eq.gk2
		} else {
			canonicalGK, aliasGK = eq.gk2, eq.gk1
		}
		anyGKV := tracker[canonicalGK][0]
		canonicalGKV := reg[anyGKV].canonical
		for _, gkv := range tracker[aliasGK] {
			reg[gkv] = &gvkInfo{
				canonical: canonicalGKV,
				resource:  reg[gkv].resource,
			}
		}
	}

	sm.registry = reg
	if sm.verbosity > 0 {
		var display []string
		for k, v := range reg {
			l := fmt.Sprintf("%s/%s:%s", k.Group, k.Version, k.Kind)
			r := fmt.Sprintf("%s/%s:%s", v.canonical.Group, v.canonical.Version, v.canonical.Kind)
			ns := "cluster scoped"
			if v.resource.Namespaced {
				ns = "namespaced"
			}
			display = append(display, fmt.Sprintf("\t%-70s => %s (%s)", l, r, ns))
		}
		sort.Strings(display)
		sio.Debugln()
		sio.Debugln("group version kind map:")
		for _, line := range display {
			sio.Debugln(line)
		}
		sio.Debugln()
	}

	duration := time.Now().Sub(start).Round(time.Millisecond)
	sio.Debugln("cluster metadata load took", duration)
	return nil
}

func (sm *ServerMetadata) openAPIResources() (openapi.Resources, *validators, error) {
	sm.ol.Lock()
	defer sm.ol.Unlock()
	ret := sm.oResult
	if ret != nil {
		return ret.res, ret.validators, ret.err
	}
	handle := func(r openapi.Resources, err error) (openapi.Resources, *validators, error) {
		sm.oResult = &openapiResourceResult{res: r, err: err}
		if err == nil {
			sm.oResult.validators = &validators{
				res:   r,
				cache: map[schema.GroupVersionKind]*schemaResult{},
			}
		}
		return sm.oResult.res, sm.oResult.validators, sm.oResult.err
	}
	doc, err := sm.disco.OpenAPISchema()
	if err != nil {
		return handle(nil, errors.Wrap(err, "Open API doc from server"))
	}
	res, err := openapi.NewOpenAPIData(doc)
	if err != nil {
		return handle(nil, errors.Wrap(err, "get resources from validator"))
	}
	return handle(res, nil)
}
