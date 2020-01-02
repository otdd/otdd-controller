/*
Copyright 2017 The Kubernetes Authors.

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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Recorder is a specification for a Recorder resource
type Recorder struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RecorderSpec   `json:"spec"`
	Status RecorderStatus `json:"status"`
}

// RecorderSpec is the spec for a Recorder resource
type RecorderSpec struct {
	TargetDeployment string `json:"targetDeployment"`
	Port int32 `json:"port"`
	Protocol string `json:"protocol"`
}

// RecorderStatus is the status for a Recorder resource
type RecorderStatus struct {
	Installed int32 `json:"installed"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// RecorderList is a list of Recorder resources
type RecorderList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Recorder `json:"items"`
}
