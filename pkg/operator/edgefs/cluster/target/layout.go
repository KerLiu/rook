/*
Copyright 2019 The Rook Authors. All rights reserved.

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

package target

import (
	"fmt"
	"path/filepath"
	"strings"

	edgefsv1alpha1 "github.com/rook/rook/pkg/apis/edgefs.rook.io/v1alpha1"
	rookalpha "github.com/rook/rook/pkg/apis/rook.io/v1alpha2"
	"github.com/rook/rook/pkg/operator/edgefs/cluster/target/config"
	"github.com/rook/rook/pkg/util/sys"
)

// CreateQualifiedHeadlessServiceName creates a qualified name of the headless service for a given replica id and namespace,
// e.g., edgefs-0.edgefs.rook-edgefs
func CreateQualifiedHeadlessServiceName(replicaNum int, namespace string) string {
	return fmt.Sprintf("%s-%d.%s.%s", appName, replicaNum, appName, namespace)
}

func getIdDevLinkName(dls string) (dl string) {
	dlsArr := strings.Split(dls, " ")
	for i := range dlsArr {
		s := strings.Replace(dlsArr[i], "/dev/disk/by-id/", "", 1)
		if strings.Contains(s, "/") || strings.Contains(s, "wwn-") {
			continue
		}
		dl = s
		break
	}
	return dl
}

func GetRTDevices(nodeDisks []sys.LocalDisk, storeConfig *config.StoreConfig) (rtDevices []edgefsv1alpha1.RTDevice, err error) {
	rtDevices = make([]edgefsv1alpha1.RTDevice, 0)
	if storeConfig == nil {
		return rtDevices, fmt.Errorf("no pointer to StoreConfig provided")
	}

	if len(nodeDisks) == 0 {
		return rtDevices, nil
	}

	var ssds []sys.LocalDisk
	var hdds []sys.LocalDisk
	var devices []sys.LocalDisk

	for i := range nodeDisks {
		if !nodeDisks[i].Empty || len(nodeDisks[i].Partitions) > 0 {
			continue
		}
		if nodeDisks[i].Rotational {
			hdds = append(hdds, nodeDisks[i])
		} else {
			ssds = append(ssds, nodeDisks[i])
		}
		devices = append(devices, nodeDisks[i])
	}

	//var rtdevs []RTDevice
	if storeConfig.UseAllSSD {
		//
		// All flush media case (High Performance)
		//
		if len(ssds) == 0 {
			return rtDevices, fmt.Errorf("No SSD/NVMe media found")
		}
		if storeConfig.UseMetadataOffload {
			fmt.Println("Warning: useMetadataOffload parameter is ignored due to use useAllSSD=true")
		}

		for i := range devices {
			if devices[i].Rotational {
				continue
			}
			rtdev := edgefsv1alpha1.RTDevice{
				Name:       getIdDevLinkName(devices[i].DevLinks),
				Device:     "/dev/" + devices[i].Name,
				Psize:      storeConfig.LmdbPageSize,
				VerifyChid: storeConfig.RtVerifyChid,
				Sync:       storeConfig.Sync,
			}
			if storeConfig.RtPLevelOverride != 0 {
				rtdev.PlevelOverride = storeConfig.RtPLevelOverride
			}
			rtDevices = append(rtDevices, rtdev)
		}
		return rtDevices, nil
	}

	if len(hdds) == 0 {
		return rtDevices, fmt.Errorf("No HDD media found")
	}

	if !storeConfig.UseMetadataOffload {
		//
		// All HDD media case (capacity, cold archive)
		//
		for i := range devices {
			if !devices[i].Rotational {
				continue
			}
			rtdev := edgefsv1alpha1.RTDevice{
				Name:       getIdDevLinkName(devices[i].DevLinks),
				Device:     "/dev/" + devices[i].Name,
				Psize:      storeConfig.LmdbPageSize,
				VerifyChid: storeConfig.RtVerifyChid,
				Sync:       storeConfig.Sync,
			}
			if storeConfig.RtPLevelOverride != 0 {
				rtdev.PlevelOverride = storeConfig.RtPLevelOverride
			}
			rtDevices = append(rtDevices, rtdev)
		}
		return rtDevices, nil
	}

	//
	// Hybrid SSD/HDD media case (optimal)
	//
	if len(hdds) < len(ssds) || len(ssds) == 0 {
		return rtDevices, fmt.Errorf("Confusing use of useMetadataOffload parameter HDDs(%d) < SSDs(%d)\n", len(hdds), len(ssds))
	}

	var hdds_divided [][]sys.LocalDisk
	for i := len(ssds); i > 0; i-- {
		chunkSize := len(hdds) / i
		mod := len(hdds) % i
		if mod > 0 {
			chunkSize++
		}

		if len(hdds) < chunkSize {
			chunkSize = len(hdds)
		}
		hdds_divided = append(hdds_divided, hdds[:chunkSize])
		hdds = hdds[chunkSize:]
	}

	for i := range hdds_divided {
		for j := range hdds_divided[i] {
			rtdev := edgefsv1alpha1.RTDevice{
				Name:              getIdDevLinkName(hdds_divided[i][j].DevLinks),
				Device:            "/dev/" + hdds_divided[i][j].Name,
				Psize:             storeConfig.LmdbPageSize,
				VerifyChid:        storeConfig.RtVerifyChid,
				BcacheWritearound: (map[bool]int{true: 0, false: 1})[storeConfig.UseBCacheWB],
				Journal:           getIdDevLinkName(ssds[i].DevLinks),
				Metadata:          getIdDevLinkName(ssds[i].DevLinks) + "," + storeConfig.UseMetadataMask,
				Bcache:            0,
				Sync:              storeConfig.Sync,
			}

			if storeConfig.UseBCache {
				rtdev.Bcache = 1
				if storeConfig.UseBCacheWB {
					rtdev.BcacheWritearound = 0
				}
			}

			if storeConfig.RtPLevelOverride != 0 {
				rtdev.PlevelOverride = storeConfig.RtPLevelOverride
			}
			rtDevices = append(rtDevices, rtdev)
		}
	}
	return rtDevices, nil
}

func GetRtlfsDevices(directories []rookalpha.Directory, storeConfig *config.StoreConfig) []edgefsv1alpha1.RtlfsDevice {
	rtlfsDevices := make([]edgefsv1alpha1.RtlfsDevice, 0)
	for _, dir := range directories {
		rtlfsDevice := edgefsv1alpha1.RtlfsDevice{
			Name:            filepath.Base(dir.Path),
			Path:            dir.Path,
			CheckMountpoint: 0,
			Psize:           storeConfig.LmdbPageSize,
			VerifyChid:      storeConfig.RtVerifyChid,
			Sync:            storeConfig.Sync,
		}
		if storeConfig.MaxSize != 0 {
			rtlfsDevice.Maxsize = storeConfig.MaxSize
		}
		if storeConfig.RtPLevelOverride != 0 {
			rtlfsDevice.PlevelOverride = storeConfig.RtPLevelOverride
		}
		rtlfsDevices = append(rtlfsDevices, rtlfsDevice)
	}
	return rtlfsDevices
}
