/*
Copyright 2019 The Kubernetes Authors.

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

// Code generated by lister-gen. DO NOT EDIT.

package v1alpha1

import (
	v1alpha1 "pod-service-crd/pkg/apis/podservicecrd/v1alpha1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
)

// PodServiceLister helps list PodServices.
type PodServiceLister interface {
	// List lists all PodServices in the indexer.
	List(selector labels.Selector) (ret []*v1alpha1.PodService, err error)
	// PodServices returns an object that can list and get PodServices.
	PodServices(namespace string) PodServiceNamespaceLister
	PodServiceListerExpansion
}

// podServiceLister implements the PodServiceLister interface.
type podServiceLister struct {
	indexer cache.Indexer
}

// NewPodServiceLister returns a new PodServiceLister.
func NewPodServiceLister(indexer cache.Indexer) PodServiceLister {
	return &podServiceLister{indexer: indexer}
}

// List lists all PodServices in the indexer.
func (s *podServiceLister) List(selector labels.Selector) (ret []*v1alpha1.PodService, err error) {
	err = cache.ListAll(s.indexer, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.PodService))
	})
	return ret, err
}

// PodServices returns an object that can list and get PodServices.
func (s *podServiceLister) PodServices(namespace string) PodServiceNamespaceLister {
	return podServiceNamespaceLister{indexer: s.indexer, namespace: namespace}
}

// PodServiceNamespaceLister helps list and get PodServices.
type PodServiceNamespaceLister interface {
	// List lists all PodServices in the indexer for a given namespace.
	List(selector labels.Selector) (ret []*v1alpha1.PodService, err error)
	// Get retrieves the PodService from the indexer for a given namespace and name.
	Get(name string) (*v1alpha1.PodService, error)
	PodServiceNamespaceListerExpansion
}

// podServiceNamespaceLister implements the PodServiceNamespaceLister
// interface.
type podServiceNamespaceLister struct {
	indexer   cache.Indexer
	namespace string
}

// List lists all PodServices in the indexer for a given namespace.
func (s podServiceNamespaceLister) List(selector labels.Selector) (ret []*v1alpha1.PodService, err error) {
	err = cache.ListAllByNamespace(s.indexer, s.namespace, selector, func(m interface{}) {
		ret = append(ret, m.(*v1alpha1.PodService))
	})
	return ret, err
}

// Get retrieves the PodService from the indexer for a given namespace and name.
func (s podServiceNamespaceLister) Get(name string) (*v1alpha1.PodService, error) {
	obj, exists, err := s.indexer.GetByKey(s.namespace + "/" + name)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, errors.NewNotFound(v1alpha1.Resource("podservice"), name)
	}
	return obj.(*v1alpha1.PodService), nil
}
