/*
Copyright 2019 The Knative Authors.

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

package v1alpha1

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// CloudEventResource is an event sink to which events are delivered when a Task has finished
type CloudEventResource struct {
	Name           string               `json:"name"`
  Type           PipelineResourceType `json:"type"`
	TargetURI      string 							`json:"targetURI"`
	Owner 				 TaskRun	            `json:"taskRun"`
}

// NewCloudEventResource creates a new GCS resource to pass to a Task
func NewCloudEventResource(r *PipelineResource) (*CloudEventResource, error) {
	if r.Spec.Type != PipelineResourceTypeCloudEvent {
		return nil, fmt.Errorf("CloudEventResource: Cannot create a Cloud Event resource from a %s Pipeline Resource", r.Spec.Type)
	}
	var targetURI string
	var targetURISpecified bool

	for _, param := range r.Spec.Params {
		switch {
		case strings.EqualFold(param.Name, "TargetURI"):
			targetURI = param.Value
			if param.Value != "" {
				targetURISpecified = true
			}
		}
	}

	if !targetURISpecified {
		return nil, fmt.Errorf("CloudEventResource: Need URI to be specified in order to create a CloudEvent resource %s", r.Name)
	}
	return &CloudEventResource{
		Name:        r.Name,
		Type:        r.Spec.Type,
		TargetURI: 	 targetURI,
	}, nil
}

// GetName returns the name of the resource
func (s CloudEventResource) GetName() string {
	return s.Name
}

// GetType returns the type of the resource, in this case "storage"
func (s CloudEventResource) GetType() PipelineResourceType {
	return PipelineResourceTypeCloudEvent
}

// SetOwner sets the owner TaskRun. PipelineResources don't have a proper owner in k8s terms, they may have more than one in theory, so we cannot use the owner reference since there is none defined
func (s *CloudEventResource) SetOwner(owner TaskRun) {
	s.Owner = owner
}

// GetParams get params
func (s *CloudEventResource) GetParams() []Param { return []Param{} }

// Replacements is used for template replacement on an CloudEventResource inside of a Taskrun.
func (s *CloudEventResource) Replacements() map[string]string {
	return map[string]string{
		"name":       s.Name,
		"type":       string(s.Type),
		"target-uri": s.TargetURI,
	}
}

// GetUploadContainerSpec returns nothing as the cloud event is sent by the controller once the POD execution is completed
func (s *CloudEventResource) GetUploadContainerSpec() ([]corev1.Container, error) {
	return nil, nil
}

// GetDownloadContainerSpec returns nothing, cloud events cannot be used as input resource
func (s *CloudEventResource) GetDownloadContainerSpec() ([]corev1.Container, error) {
	return nil, nil
}

// SetDestinationDirectory required by the interface but not really used
func (s *CloudEventResource) SetDestinationDirectory(path string) {
}
