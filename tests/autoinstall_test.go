package mos_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	. "github.com/spectrocloud/peg/matcher"
)

var stateContains = func(vm VM, query string, expected ...string) {
	or := []types.GomegaMatcher{}
	for _, e := range expected {
		or = append(or, ContainSubstring(e))
	}
	out, err := vm.Sudo(fmt.Sprintf("kairos-agent state get %s", query))
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, strings.ToLower(out)).To(Or(or...))
}

var _ = Describe("kairos autoinstall test", Label("acceptance"), func() {
	var vm VM
	var datasource string

	BeforeEach(func() {
		datasource = CreateDatasource("assets/autoinstall.yaml")
		Expect(os.Setenv("DATASOURCE", datasource)).ToNot(HaveOccurred())
		_, vm = startVM()
		vm.EventuallyConnects(1200)
	})

	AfterEach(func() {
		if CurrentSpecReport().Failed() {
			gatherLogs(vm)
			serial, _ := os.ReadFile(filepath.Join(vm.StateDir, "serial.log"))
			_ = os.MkdirAll("logs", os.ModePerm|os.ModeDir)
			_ = os.WriteFile(filepath.Join("logs", "serial.log"), serial, os.ModePerm)
			fmt.Println(string(serial))
		}

		err := vm.Destroy(nil)
		Expect(err).ToNot(HaveOccurred())

		Expect(os.Unsetenv("DATASOURCE")).ToNot(HaveOccurred())
		Expect(os.Remove(datasource)).ToNot(HaveOccurred())
	})

	Context("reboots and passes functional tests", func() {
		BeforeEach(func() {
			expectDefaultService(vm)
			expectStartedInstallation(vm)
			expectRebootedToActive(vm)
		})

		It("passes checks", func() {
			By("checking grubenv file", func() {
				out, err := vm.Sudo("cat /oem/grubenv")
				Expect(err).ToNot(HaveOccurred(), out)
				Expect(out).To(ContainSubstring("foobarzz"))
			})

			By("checking custom cmdline", func() {
				out, err := vm.Sudo("cat /proc/cmdline")
				Expect(err).ToNot(HaveOccurred())
				Expect(out).To(ContainSubstring("foobarzz"))
			})

			By("checking the use of dracut immutable module", func() {
				out, err := vm.Sudo("cat /proc/cmdline")
				Expect(err).ToNot(HaveOccurred())
				Expect(out).To(ContainSubstring("cos-img/filename="))
			})

			By("checking Auto assessment", func() {
				// Auto assessment was installed
				out, _ := vm.Sudo("cat /run/initramfs/cos-state/grubcustom")
				Expect(out).To(ContainSubstring("bootfile_loc"))

				out, _ = vm.Sudo("cat /run/initramfs/cos-state/grub_boot_assessment")
				Expect(out).To(ContainSubstring("boot_assessment_blk"))

				cmdline, _ := vm.Sudo("cat /proc/cmdline")
				Expect(cmdline).To(ContainSubstring("rd.emergency=reboot rd.shell=0"))
				Expect(cmdline).To(ContainSubstring("panic=5"))
				Expect(cmdline).To(ContainSubstring("rd.shell=0"))
			})

			By("checking writeable tmp", func() {
				_, err := vm.Sudo("echo 'foo' > /tmp/bar")
				Expect(err).ToNot(HaveOccurred())

				out, err := vm.Sudo("sudo cat /tmp/bar")
				Expect(err).ToNot(HaveOccurred())

				Expect(out).To(ContainSubstring("foo"))
			})

			By("checking bpf mount", func() {
				Eventually(func() string {
					out, _ := vm.Sudo("mount")
					return out
				}, 5*time.Minute, 1*time.Second).Should(
					Or(
						ContainSubstring("bpf"),
					))
			})

			By("checking correct permissions", func() {
				out, err := vm.Sudo(`stat -c "%a" /oem`)
				Expect(err).ToNot(HaveOccurred())
				Expect(out).To(ContainSubstring("770"))

				out, err = vm.Sudo(`stat -c "%a" /usr/local/cloud-config`)
				Expect(err).ToNot(HaveOccurred())
				Expect(out).To(ContainSubstring("770"))
			})

			By("checking grubmenu", func() {
				// Statereset is now part of the default grub.cfg
				out, err := vm.Sudo("cat /etc/cos/grub.cfg")
				Expect(err).ToNot(HaveOccurred())
				Expect(out).To(ContainSubstring("--id cos"))
				Expect(out).To(ContainSubstring("--id fallback"))
				Expect(out).To(ContainSubstring("--id recovery"))
				Expect(out).To(ContainSubstring("--id statereset"))
				// Now this one you can override with a custom grubmenu but by default we ship the remote recovery on it
				out, err = vm.Sudo("cat /run/initramfs/cos-state/grubmenu")
				Expect(err).ToNot(HaveOccurred())
				Expect(out).To(ContainSubstring("remoterecovery"))
			})

			By("checking additional mount specified, with no dir in rootfs", func() {
				out, err := vm.Sudo("mount")
				Expect(err).ToNot(HaveOccurred())
				Expect(out).To(ContainSubstring("/var/lib/longhorn"))
			})

			By("checking rootfs shared mount", func() {
				out, err := vm.Sudo(`cat /proc/1/mountinfo | grep ' / / '`)
				Expect(err).ToNot(HaveOccurred(), out)
				Expect(out).To(ContainSubstring("shared"))
			})

			By("checking that it doesn't has grub data into the cloud config", func() {
				out, err := vm.Sudo(`cat /oem/90_custom.yaml`)
				Expect(err).ToNot(HaveOccurred(), out)
				Expect(out).ToNot(ContainSubstring("vga_text"))
				Expect(out).ToNot(ContainSubstring("videotest"))
			})

			By("checking that networking is functional", func() {
				out, err := vm.Sudo(`curl google.it`)
				Expect(err).ToNot(HaveOccurred(), out)
				Expect(out).To(ContainSubstring("Moved"))
			})

			By("checking corresponding state", func() {
				out, err := vm.Sudo("kairos-agent state")
				Expect(err).ToNot(HaveOccurred())
				Expect(out).To(ContainSubstring("boot: active_boot"))
				currentVersion, err := vm.Sudo(getVersionCmd)
				Expect(err).ToNot(HaveOccurred(), currentVersion)

				stateAssertVM(vm, "oem.mounted", "true")
				stateAssertVM(vm, "oem.found", "true")
				stateAssertVM(vm, "persistent.mounted", "true")
				stateAssertVM(vm, "state.mounted", "true")
				stateAssertVM(vm, "oem.type", "ext4")
				stateAssertVM(vm, "persistent.type", "ext4")
				stateAssertVM(vm, "state.type", "ext4")
				stateAssertVM(vm, "oem.mount_point", "/oem")
				stateAssertVM(vm, "persistent.mount_point", "/usr/local")
				stateAssertVM(vm, "persistent.name", "/dev/vda")
				stateAssertVM(vm, "state.mount_point", "/run/initramfs/cos-state")
				stateAssertVM(vm, "oem.read_only", "false")
				stateAssertVM(vm, "persistent.read_only", "false")
				stateAssertVM(vm, "state.read_only", "true")
				stateAssertVM(vm, "kairos.version", strings.ReplaceAll(strings.ReplaceAll(currentVersion, "\r", ""), "\n", ""))
				stateContains(vm, "system.os.name", "alpine", "opensuse", "ubuntu", "debian")
				stateContains(vm, "kairos.flavor", "alpine", "opensuse", "ubuntu", "debian")
			})

			By("Checking install/recovery services do not exist", func() {
				if !isFlavor(vm, "alpine") {
					for _, service := range []string{"kairos-interactive", "kairos-recovery"} {
						By(fmt.Sprintf("Checking that service %s does nto exist", service), func() {})
						Eventually(func() string {
							out, _ := vm.Sudo(fmt.Sprintf("systemctl status %s", service))
							return out
						}, 3*time.Minute, 2*time.Second).Should(
							And(
								ContainSubstring(fmt.Sprintf("Unit %s.service could not be found", service)),
							),
						)
					}
				}
			})
		})
	})
})
