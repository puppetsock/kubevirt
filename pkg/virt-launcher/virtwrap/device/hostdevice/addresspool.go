/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2021 Red Hat, Inc.
 *
 */

package hostdevice

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"kubevirt.io/client-go/log"
	"kubevirt.io/kubevirt/pkg/util"
)

type AddressPool struct {
	addressesByResource map[string][]string
}

type MultusNetworkStatus []struct {
	Name    string   `json:"name"`
	Ips     []string `json:"ips"`
	Default bool     `json:"default,omitempty"`
	DNS     struct {
	} `json:"dns"`
	Interface  string `json:"interface,omitempty"`
	DeviceInfo struct {
		Type    string `json:"type"`
		Version string `json:"version"`
		Pci     struct {
			PciAddress string `json:"pci-address"`
		} `json:"pci"`
	} `json:"device-info,omitempty"`
}

// NewAddressPool creates an address pool based on the provided list of resources and
// the environment variables that correspond to it.
func NewAddressPool(resourcePrefix string, resources []string) *AddressPool {
	pool := &AddressPool{
		addressesByResource: make(map[string][]string),
	}
	pool.load(resourcePrefix, resources)
	return pool
}

func (p *AddressPool) load(resourcePrefix string, resources []string) {
	for _, resource := range resources {
		addressEnvVarName := util.ResourceNameToEnvVar(resourcePrefix, resource)
		addressString, isSet := os.LookupEnv(addressEnvVarName)
		if !isSet {
			log.Log.Warningf("%s not set for resource %s", addressEnvVarName, resource)
			continue
		}

		addressString = strings.TrimSuffix(addressString, ",")
		if addressString != "" {
			p.addressesByResource[resource] = strings.Split(addressString, ",")
		} else {
			p.addressesByResource[resource] = nil
		}
	}
}

// Pop gets the next address available to a particular resource. The
// function makes sure that the allocated address is not allocated to next
// callers, whether they request an address for the same resource or another
// resource (covering cases of addresses that are share by multiple resources).
func (p *AddressPool) Pop(resource string) (string, error) {

	// the addresspool pop is called by a few different things, not only sriov
	// so we do a bit of a hack to avoid changing the interface for everything
	// by looking at the resource string to see if it might contain a hint
	// of the target network and if so, we store that
	var netname string
	if strings.Contains(resource, "|") {
		n := strings.Split(resource, "|")
		resource, netname = n[0], n[1]
	}

	addresses, exists := p.addressesByResource[resource]
	if !exists {
		return "", fmt.Errorf("resource %s does not exist", resource)
	}

	if len(addresses) > 0 {
		pickedAddresses := []string{}
		var selectedAddress string

		if netname != "" {

			multusNetworkStatusFilePath := "/run/multus-info/multus.json"
			multusNetworkStatusFile, err := os.Open(multusNetworkStatusFilePath)
			if err != nil {
				fmt.Println(err)
			}
			defer multusNetworkStatusFile.Close()

			// read our opened multusNetworkStatusFile as a byte array.
			byteValue, _ := ioutil.ReadAll(multusNetworkStatusFile)

			// initialize our MultusNetworkStatus array
			var multusNetworkStatus MultusNetworkStatus

			// unmarshal our byteArray which contains our
			// multusNetworkStatusFile's content into 'multusNetworkStatus' which we defined above
			json.Unmarshal(byteValue, &multusNetworkStatus)

			// iterate over the MultusNetworkStatus within our multusNetworkStatus array and
			// pick the pci addr for tne network(s) whos multus interface matches our netname
			for i := 0; i < len(multusNetworkStatus); i++ {
				if multusNetworkStatus[i].Interface == netname {
					// fmt.Println("Network Interface: " + multusNetworkStatus[i].Interface)
					// fmt.Println("Network Name: " + multusNetworkStatus[i].Name)
					// fmt.Println("Network Host PCI: " + multusNetworkStatus[i].DeviceInfo.Pci.PciAddress)
					pickedAddresses = append(pickedAddresses, multusNetworkStatus[i].DeviceInfo.Pci.PciAddress)
				}
			}

			selectedAddress = pickedAddresses[0]
			log.Log.Infof("multus sriov selected host-device %s for network %s", pickedAddresses, netname)
		} else {
			selectedAddress = addresses[0]
		}

		for resourceName, resourceAddresses := range p.addressesByResource {
			p.addressesByResource[resourceName] = filterOutAddress(resourceAddresses, selectedAddress)
		}

		return selectedAddress, nil
	}
	return "", fmt.Errorf("no more addresses to allocate for resource %s", resource)
}

func filterOutAddress(addrs []string, addr string) []string {
	var res []string
	for _, a := range addrs {
		if a != addr {
			res = append(res, a)
		}
	}
	return res
}

type BestEffortAddressPool struct {
	pool AddressPooler
}

// NewBestEffortAddressPool creates a pool that wraps a provided pool
// and allows `Pop` calls to always succeed (even when a resource is missing).
func NewBestEffortAddressPool(pool AddressPooler) *BestEffortAddressPool {
	return &BestEffortAddressPool{pool}
}

func (p *BestEffortAddressPool) Pop(resource string) (string, error) {
	address, _ := p.pool.Pop(resource)
	return address, nil
}
