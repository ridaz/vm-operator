// Copyright (c) 2024 VMware, Inc. All Rights Reserved.
// SPDX-License-Identifier: Apache-2.0

package resize

import (
	"context"
	"reflect"
	"slices"

	"github.com/vmware/govmomi/object"
	vimtypes "github.com/vmware/govmomi/vim25/types"

	"github.com/vmware-tanzu/vm-operator/pkg/providers/vsphere/virtualmachine"
	pkgutil "github.com/vmware-tanzu/vm-operator/pkg/util"
)

// CreateResizeConfigSpec takes the current VM state in the ConfigInfo and compares it to the
// desired state in the ConfigSpec, returning a ConfigSpec with any required changes to drive
// the desired state.
func CreateResizeConfigSpec(
	_ context.Context,
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec) (vimtypes.VirtualMachineConfigSpec, error) {

	outCS := vimtypes.VirtualMachineConfigSpec{}

	compareAnnotation(ci, cs, &outCS)
	compareManagedBy(ci, cs, &outCS)
	compareHardware(ci, cs, &outCS)
	compareCPUAllocation(ci, cs, &outCS)
	compareCPUHotAddOrRemove(ci, cs, &outCS)
	compareCPUAffinity(ci, cs, &outCS)
	compareCPUPerfCounter(ci, cs, &outCS)
	compareLatencySensitivity(ci, cs, &outCS)
	compareExtraConfig(ci, cs, &outCS)
	compareConsolePreferences(ci, cs, &outCS)
	compareFlags(ci, cs, &outCS)
	compareMemoryAllocation(ci, cs, &outCS)
	compareMemoryHotAdd(ci, cs, &outCS)
	compareFixedPassthruHotPlug(ci, cs, &outCS)
	compareNestedHVEnabled(ci, cs, &outCS)
	compareSevEnabled(ci, cs, &outCS)
	compareVmxStatsCollectionEnabled(ci, cs, &outCS)

	return outCS, nil
}

// compareAnnotation compares the ConfigInfo.Annotation.
func compareAnnotation(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {

	if ci.Annotation == "" {
		// Only change the Annotation if it is currently unset.
		outCS.Annotation = cs.Annotation
	}
}

// compareManagedBy compares the ConfigInfo.ManagedBy.
func compareManagedBy(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {

	if ci.ManagedBy == nil {
		// Only change the ManagedBy if it is currently unset.
		outCS.ManagedBy = cs.ManagedBy
	}
}

// compareHardware compares the ConfigInfo.Hardware.
func compareHardware(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {

	cmp(ci.Hardware.NumCPU, cs.NumCPUs, &outCS.NumCPUs)
	cmp(ci.Hardware.NumCoresPerSocket, cs.NumCoresPerSocket, &outCS.NumCoresPerSocket)
	// outCS.AutoCoresPerSocket = ...
	cmp(int64(ci.Hardware.MemoryMB), cs.MemoryMB, &outCS.MemoryMB)
	cmpPtr(ci.Hardware.VirtualICH7MPresent, cs.VirtualICH7MPresent, &outCS.VirtualICH7MPresent)
	cmpPtr(ci.Hardware.VirtualSMCPresent, cs.VirtualSMCPresent, &outCS.VirtualSMCPresent)
	cmp(ci.Hardware.MotherboardLayout, cs.MotherboardLayout, &outCS.MotherboardLayout)
	cmp(ci.Hardware.SimultaneousThreads, cs.SimultaneousThreads, &outCS.SimultaneousThreads)

	compareHardwareDevices(ci, cs, outCS)
}

func compareHardwareDevices(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {

	// The VM's current virtual devices.
	deviceList := object.VirtualDeviceList(ci.Hardware.Device)
	// The VM's desired virtual devices.
	csDeviceList := pkgutil.DevicesFromConfigSpec(&cs)

	var deviceChanges []vimtypes.BaseVirtualDeviceConfigSpec

	pciDeviceChanges := comparePCIDevices(deviceList, csDeviceList)
	deviceChanges = append(deviceChanges, pciDeviceChanges...)

	outCS.DeviceChange = deviceChanges
}

func comparePCIDevices(
	currentPCIDevices, desiredPCIDevices []vimtypes.BaseVirtualDevice) []vimtypes.BaseVirtualDeviceConfigSpec {

	currentPassthruPCIDevices := pkgutil.SelectVirtualPCIPassthrough(currentPCIDevices)

	pciPassthruFromConfigSpec := pkgutil.SelectVirtualPCIPassthrough(desiredPCIDevices)
	expectedPCIDevices := virtualmachine.CreatePCIDevicesFromConfigSpec(pciPassthruFromConfigSpec)

	var deviceChanges []vimtypes.BaseVirtualDeviceConfigSpec
	for _, expectedDev := range expectedPCIDevices {
		expectedPci := expectedDev.(*vimtypes.VirtualPCIPassthrough)
		expectedBacking := expectedPci.Backing
		expectedBackingType := reflect.TypeOf(expectedBacking)

		var matchingIdx = -1
		for idx, curDev := range currentPassthruPCIDevices {
			curBacking := curDev.GetVirtualDevice().Backing
			if curBacking == nil || reflect.TypeOf(curBacking) != expectedBackingType {
				continue
			}

			var backingMatch bool
			switch a := curBacking.(type) {
			case *vimtypes.VirtualPCIPassthroughVmiopBackingInfo:
				b := expectedBacking.(*vimtypes.VirtualPCIPassthroughVmiopBackingInfo)
				backingMatch = a.Vgpu == b.Vgpu

			case *vimtypes.VirtualPCIPassthroughDynamicBackingInfo:
				currAllowedDevs := a.AllowedDevice
				b := expectedBacking.(*vimtypes.VirtualPCIPassthroughDynamicBackingInfo)
				if a.CustomLabel == b.CustomLabel && len(b.AllowedDevice) > 0 {
					// b.AllowedDevice has only one element because CreatePCIDevices() adds only one device based
					// on the devices listed in vmclass.spec.hardware.devices.dynamicDirectPathIODevices.
					expectedAllowedDev := b.AllowedDevice[0]
					for i := 0; i < len(currAllowedDevs) && !backingMatch; i++ {
						backingMatch = expectedAllowedDev.DeviceId == currAllowedDevs[i].DeviceId &&
							expectedAllowedDev.VendorId == currAllowedDevs[i].VendorId
					}
				}
			}

			if backingMatch {
				matchingIdx = idx
				break
			}
		}

		if matchingIdx == -1 {
			deviceChanges = append(deviceChanges, &vimtypes.VirtualDeviceConfigSpec{
				Operation: vimtypes.VirtualDeviceConfigSpecOperationAdd,
				Device:    expectedPci,
			})
		} else {
			// There could be multiple vGPUs with same BackingInfo. Remove current device if matching found.
			currentPassthruPCIDevices = append(currentPassthruPCIDevices[:matchingIdx], currentPassthruPCIDevices[matchingIdx+1:]...)
		}
	}
	// Remove any unmatched existing devices.
	removeDeviceChanges := make([]vimtypes.BaseVirtualDeviceConfigSpec, 0, len(currentPassthruPCIDevices))
	for _, dev := range currentPassthruPCIDevices {
		removeDeviceChanges = append(removeDeviceChanges, &vimtypes.VirtualDeviceConfigSpec{
			Operation: vimtypes.VirtualDeviceConfigSpecOperationRemove,
			Device:    dev,
		})
	}

	// Process any removes first.
	return append(removeDeviceChanges, deviceChanges...)
}

// compareCPUAllocation compares CPU resource allocation.
func compareCPUAllocation(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {
	// nothing to change
	if cs.CpuAllocation == nil {
		return
	}

	ciCPUAllocation := ci.CpuAllocation
	csCPUAllocation := cs.CpuAllocation

	var cpuReservation *int64
	if csCPUAllocation.Reservation != nil {
		if ciCPUAllocation == nil || ciCPUAllocation.Reservation == nil || *ciCPUAllocation.Reservation != *csCPUAllocation.Reservation {
			cpuReservation = csCPUAllocation.Reservation
		}
	}

	var cpuLimit *int64
	if csCPUAllocation.Limit != nil {
		if ciCPUAllocation == nil || ciCPUAllocation.Limit == nil || *ciCPUAllocation.Limit != *csCPUAllocation.Limit {
			cpuLimit = csCPUAllocation.Limit
		}
	}

	var cpuShares *vimtypes.SharesInfo
	if csCPUAllocation.Shares != nil {
		if ciCPUAllocation == nil || ciCPUAllocation.Shares == nil ||
			ciCPUAllocation.Shares.Level != csCPUAllocation.Shares.Level ||
			(csCPUAllocation.Shares.Level == vimtypes.SharesLevelCustom && ciCPUAllocation.Shares.Shares != csCPUAllocation.Shares.Shares) {
			cpuShares = csCPUAllocation.Shares
		}
	}

	if cpuReservation != nil || cpuLimit != nil || cpuShares != nil {
		outCS.CpuAllocation = &vimtypes.ResourceAllocationInfo{}

		if cpuReservation != nil {
			outCS.CpuAllocation.Reservation = cpuReservation
		}

		if cpuLimit != nil {
			outCS.CpuAllocation.Limit = cpuLimit
		}

		if cpuShares != nil {
			outCS.CpuAllocation.Shares = cpuShares
		}
	}
}

// compareCPUHotAddOrRemove compares CPU hot add and remove enabled.
func compareCPUHotAddOrRemove(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {
	cmpPtr(ci.CpuHotAddEnabled, cs.CpuHotAddEnabled, &outCS.CpuHotAddEnabled)
	cmpPtr(ci.CpuHotRemoveEnabled, cs.CpuHotRemoveEnabled, &outCS.CpuHotRemoveEnabled)
}

// compareCPUPerfCounter compares virtual CPU performance counter enablement.
func compareCPUPerfCounter(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {
	cmpPtr(ci.VPMCEnabled, cs.VPMCEnabled, &outCS.VPMCEnabled)
}

// compareCPUAffinity compares CPU affinity settings in ConfigSpec.
func compareCPUAffinity(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {

	if cs.CpuAffinity == nil {
		return
	}

	if ci.CpuAffinity == nil {
		outCS.CpuAffinity = cs.CpuAffinity
	}

	if ci.CpuAffinity != nil {
		slices.Sort(ci.CpuAffinity.AffinitySet)
		slices.Sort(cs.CpuAffinity.AffinitySet)
		if !reflect.DeepEqual(ci.CpuAffinity.AffinitySet, cs.CpuAffinity.AffinitySet) {
			outCS.CpuAffinity = cs.CpuAffinity
		}
	}
}

// compareLatencySensitivity compares the latency-sensitivity of the virtual machine.
func compareLatencySensitivity(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {

	if cs.LatencySensitivity == nil {
		return
	}

	if ci.LatencySensitivity == nil ||
		ci.LatencySensitivity.Sensitivity != cs.LatencySensitivity.Sensitivity ||
		ci.LatencySensitivity.Level != cs.LatencySensitivity.Level {
		outCS.LatencySensitivity = &vimtypes.LatencySensitivity{
			Level: cs.LatencySensitivity.Level,
			// deprecated since vsphere 5.5
			//Sensitivity: cs.LatencySensitivity.Sensitivity,
		}
	}
}

// compareExtraConfig compares the extra config setting in the Config Spec to add new keys
// or updates values for existing keys.
func compareExtraConfig(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {

	outCS.ExtraConfig = pkgutil.OptionValues(ci.ExtraConfig).Diff(cs.ExtraConfig...)
}

// compareConsolePreferences compares the console preferences settings in the Config Spec.
func compareConsolePreferences(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {

	if cs.ConsolePreferences == nil {
		return
	}

	if ci.ConsolePreferences == nil {
		// If configInfo console preferences is nil, there is nothing to compare.
		// set desired values from configSpec if they are non-nil.
		if cs.ConsolePreferences.PowerOnWhenOpened != nil ||
			cs.ConsolePreferences.CloseOnPowerOffOrSuspend != nil ||
			cs.ConsolePreferences.EnterFullScreenOnPowerOn != nil {
			outCS.ConsolePreferences = &vimtypes.VirtualMachineConsolePreferences{
				PowerOnWhenOpened:        cs.ConsolePreferences.PowerOnWhenOpened,
				EnterFullScreenOnPowerOn: cs.ConsolePreferences.EnterFullScreenOnPowerOn,
				CloseOnPowerOffOrSuspend: cs.ConsolePreferences.CloseOnPowerOffOrSuspend,
			}
		}

		return
	}

	// If both configInfo and configSpec have non-nil console preferences, compare and set
	// the desired.
	outCS.ConsolePreferences = &vimtypes.VirtualMachineConsolePreferences{}
	cmpPtr(ci.ConsolePreferences.PowerOnWhenOpened, cs.ConsolePreferences.PowerOnWhenOpened, &outCS.ConsolePreferences.PowerOnWhenOpened)
	cmpPtr(ci.ConsolePreferences.EnterFullScreenOnPowerOn, cs.ConsolePreferences.EnterFullScreenOnPowerOn, &outCS.ConsolePreferences.EnterFullScreenOnPowerOn)
	cmpPtr(ci.ConsolePreferences.CloseOnPowerOffOrSuspend, cs.ConsolePreferences.CloseOnPowerOffOrSuspend, &outCS.ConsolePreferences.CloseOnPowerOffOrSuspend)

	// if desired preferences has all nil (ie) there was no change, nil out the console preferences to prevent unwanted reconfigures.
	if reflect.DeepEqual(outCS.ConsolePreferences, &vimtypes.VirtualMachineConsolePreferences{}) {
		outCS.ConsolePreferences = nil
	}
}

// compareFlags compares the flag info settings in the Config Spec.
func compareFlags(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {
	if cs.Flags == nil {
		return
	}

	outCS.Flags = &vimtypes.VirtualMachineFlagInfo{}
	cmpPtr(ci.Flags.CbrcCacheEnabled, cs.Flags.CbrcCacheEnabled, &outCS.Flags.CbrcCacheEnabled)
	cmpPtr(ci.Flags.DisableAcceleration, cs.Flags.DisableAcceleration, &outCS.Flags.DisableAcceleration)
	cmpPtr(ci.Flags.DiskUuidEnabled, cs.Flags.DiskUuidEnabled, &outCS.Flags.DiskUuidEnabled)
	cmpPtr(ci.Flags.EnableLogging, cs.Flags.EnableLogging, &outCS.Flags.EnableLogging)
	cmpPtr(ci.Flags.UseToe, cs.Flags.UseToe, &outCS.Flags.UseToe)
	// TODO: re-eval if VvtdEnabled, VbsEnabled is allowed. Setting them to true requires 'efi' firmware
	cmpPtr(ci.Flags.VvtdEnabled, cs.Flags.VvtdEnabled, &outCS.Flags.VvtdEnabled)
	cmpPtr(ci.Flags.VbsEnabled, cs.Flags.VbsEnabled, &outCS.Flags.VbsEnabled)

	cmp(ci.Flags.MonitorType, cs.Flags.MonitorType, &outCS.Flags.MonitorType)
	cmp(ci.Flags.VirtualMmuUsage, cs.Flags.VirtualMmuUsage, &outCS.Flags.VirtualMmuUsage)
	cmp(ci.Flags.VirtualExecUsage, cs.Flags.VirtualExecUsage, &outCS.Flags.VirtualExecUsage)

	// Note: Flags not yet supported on reconfigure via VM Service
	// - SnapshotLocked, SnapshotPowerOffBehavior: Snapshotting is not yet supported on VM Service
	// - FaultToleranceType: Flag not supported on VM Service.

	if reflect.DeepEqual(outCS.Flags, &vimtypes.VirtualMachineFlagInfo{}) {
		outCS.Flags = nil
	}
}

// compareMemoryAllocation compares Memory resource allocation.
func compareMemoryAllocation(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {
	// nothing to change
	if cs.MemoryAllocation == nil {
		return
	}

	ciMemoryAllocation := ci.MemoryAllocation
	csMemoryAllocation := cs.MemoryAllocation

	var memReservation *int64
	if csMemoryAllocation.Reservation != nil {
		if ciMemoryAllocation == nil || ciMemoryAllocation.Reservation == nil || *ciMemoryAllocation.Reservation != *csMemoryAllocation.Reservation {
			memReservation = csMemoryAllocation.Reservation
		}
	}

	var memLimit *int64
	if csMemoryAllocation.Limit != nil {
		if ciMemoryAllocation == nil || ciMemoryAllocation.Limit == nil || *ciMemoryAllocation.Limit != *csMemoryAllocation.Limit {
			memLimit = csMemoryAllocation.Limit
		}
	}

	var memShares *vimtypes.SharesInfo
	if csMemoryAllocation.Shares != nil {
		if ciMemoryAllocation == nil || ciMemoryAllocation.Shares == nil ||
			ciMemoryAllocation.Shares.Level != csMemoryAllocation.Shares.Level ||
			(csMemoryAllocation.Shares.Level == vimtypes.SharesLevelCustom && ciMemoryAllocation.Shares.Shares != csMemoryAllocation.Shares.Shares) {
			memShares = csMemoryAllocation.Shares
		}
	}

	if memReservation != nil || memLimit != nil || memShares != nil {
		outCS.MemoryAllocation = &vimtypes.ResourceAllocationInfo{}

		if memReservation != nil {
			outCS.MemoryAllocation.Reservation = memReservation
		}

		if memLimit != nil {
			outCS.MemoryAllocation.Limit = memLimit
		}

		if memShares != nil {
			outCS.MemoryAllocation.Shares = memShares
		}
	}
}

func compareMemoryHotAdd(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {
	cmpPtr(ci.MemoryHotAddEnabled, cs.MemoryHotAddEnabled, &outCS.MemoryHotAddEnabled)
}

// compareFixedPassthruHotPlug compares the fixed pass-through hot plug enabled setting in the config spec.
func compareFixedPassthruHotPlug(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {
	cmpPtr(ci.FixedPassthruHotPlugEnabled, cs.FixedPassthruHotPlugEnabled, &outCS.FixedPassthruHotPlugEnabled)
}

// compareNestedHVEnabled compares the nested hardware-assisted virtualization setting in the config spec.
func compareNestedHVEnabled(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {
	cmpPtr(ci.NestedHVEnabled, cs.NestedHVEnabled, &outCS.NestedHVEnabled)
}

// compareSevEnabled compare the SEV (Secure Encryption Virtualization) setting in the config spec.
func compareSevEnabled(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {
	cmpPtr(ci.SevEnabled, cs.SevEnabled, &outCS.SevEnabled)
}

// compareVmxStatsCollectionEnabled compares the VMX stats collection setting in the config spec.
func compareVmxStatsCollectionEnabled(
	ci vimtypes.VirtualMachineConfigInfo,
	cs vimtypes.VirtualMachineConfigSpec,
	outCS *vimtypes.VirtualMachineConfigSpec) {
	cmpPtr(ci.VmxStatsCollectionEnabled, cs.VmxStatsCollectionEnabled, &outCS.VmxStatsCollectionEnabled)
}

func cmp[T comparable](a, b T, c *T) {
	if a != b {
		*c = b
	}
}

func cmpPtr[T comparable](a *T, b *T, c **T) {
	if (a == nil || b == nil) || *a != *b {
		*c = b
	}
}
