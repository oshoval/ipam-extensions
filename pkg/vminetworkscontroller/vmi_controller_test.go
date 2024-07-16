package vminetworkscontroller_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	virtv1 "kubevirt.io/api/core/v1"

	"github.com/kubevirt/ipam-extensions/pkg/testobjects"
	"github.com/kubevirt/ipam-extensions/pkg/vminetworkscontroller"

	ipamclaimsapi "github.com/k8snetworkplumbingwg/ipamclaims/pkg/crd/ipamclaims/v1alpha1"
	nadv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	"github.com/kubevirt/ipam-extensions/pkg/claims"
)

func TestController(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "VMI Controller test suite")
}

var (
	testEnv *envtest.Environment
)

type testConfig struct {
	inputVM            *virtv1.VirtualMachine
	inputVMI           *virtv1.VirtualMachineInstance
	inputNAD           *nadv1.NetworkAttachmentDefinition
	existingIPAMClaim  *ipamclaimsapi.IPAMClaim
	expectedError      error
	expectedResponse   reconcile.Result
	expectedIPAMClaims []ipamclaimsapi.IPAMClaim
}

const dummyUID = "dummyUID"

var _ = Describe("VMI IPAM controller", Serial, func() {
	BeforeEach(func() {
		log.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
		testEnv = &envtest.Environment{}
		_, err := testEnv.Start()
		Expect(err).NotTo(HaveOccurred())

		Expect(virtv1.AddToScheme(scheme.Scheme)).To(Succeed())
		Expect(nadv1.AddToScheme(scheme.Scheme)).To(Succeed())
		Expect(ipamclaimsapi.AddToScheme(scheme.Scheme)).To(Succeed())
		// +kubebuilder:scaffold:scheme
	})

	AfterEach(func() {
		Expect(testEnv.Stop()).To(Succeed())
	})

	const (
		nadName       = "ns1/superdupernad"
		namespace     = "ns1"
		vmName        = "vm1"
		unexpectedUID = "unexpectedUID"
	)

	DescribeTable("reconcile behavior is as expected", func(config testConfig) {
		var initialObjects []client.Object

		if config.inputVM != nil {
			initialObjects = append(initialObjects, config.inputVM)
		}

		var vmiKey apitypes.NamespacedName
		if config.inputVMI != nil {
			vmiKey = apitypes.NamespacedName{
				Namespace: config.inputVMI.Namespace,
				Name:      config.inputVMI.Name,
			}
			initialObjects = append(initialObjects, config.inputVMI)
		}

		if config.inputNAD != nil {
			initialObjects = append(initialObjects, config.inputNAD)
		}

		if config.existingIPAMClaim != nil {
			initialObjects = append(initialObjects, config.existingIPAMClaim)
		}

		if vmiKey.Namespace == "" && vmiKey.Name == "" {
			// must apply some default for the VMI DEL scenarios ...
			vmiKey = apitypes.NamespacedName{
				Namespace: namespace,
				Name:      vmName,
			}
		}

		ctrlOptions := controllerruntime.Options{
			Scheme: scheme.Scheme,
			NewClient: func(_ *rest.Config, _ client.Options) (client.Client, error) {
				return fake.NewClientBuilder().
					WithScheme(scheme.Scheme).
					WithObjects(initialObjects...).
					Build(), nil
			},
		}

		mgr, err := controllerruntime.NewManager(&rest.Config{}, ctrlOptions)
		Expect(err).NotTo(HaveOccurred())

		reconcileVMI := vminetworkscontroller.NewVMIReconciler(mgr)
		if config.expectedError != nil {
			_, err := reconcileVMI.Reconcile(context.Background(), controllerruntime.Request{NamespacedName: vmiKey})
			Expect(err).To(MatchError(config.expectedError))
		} else {
			Expect(
				reconcileVMI.Reconcile(context.Background(), controllerruntime.Request{NamespacedName: vmiKey}),
			).To(Equal(config.expectedResponse))
		}

		if len(config.expectedIPAMClaims) > 0 {
			ipamClaimList := &ipamclaimsapi.IPAMClaimList{}

			Expect(mgr.GetClient().List(context.Background(), ipamClaimList, claims.OwnedByVMLabel(vmName))).To(Succeed())
			Expect(testobjects.IpamClaimsCleaner(ipamClaimList.Items...)).To(ConsistOf(config.expectedIPAMClaims))
		}
	},
		Entry("when the VM has an associated VMI pointing to an existing NAD", testConfig{
			inputVM:          testobjects.DummyVM(nadName),
			inputVMI:         testobjects.DummyVMI(nadName),
			inputNAD:         testobjects.DummyNAD(nadName),
			expectedResponse: reconcile.Result{},
			expectedIPAMClaims: []ipamclaimsapi.IPAMClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:       fmt.Sprintf("%s.%s", vmName, "randomnet"),
						Namespace:  namespace,
						Finalizers: []string{claims.KubevirtVMFinalizer},
						Labels:     claims.OwnedByVMLabel(vmName),
						OwnerReferences: []metav1.OwnerReference{{
							Name:               vmName,
							Controller:         ptr.To(true),
							BlockOwnerDeletion: ptr.To(true)},
						},
					},
					Spec: ipamclaimsapi.IPAMClaimSpec{Network: "goodnet"},
				},
			},
		}),
		Entry("when the VM has an associated VMI pointing to an existing NAD with an improper config", testConfig{
			inputVM:       testobjects.DummyVM(nadName),
			inputVMI:      testobjects.DummyVMI(nadName),
			inputNAD:      dummyNADWrongFormat(nadName),
			expectedError: fmt.Errorf("failed to extract the relevant NAD information"),
		}),
		Entry("the associated VMI exists but points to a NAD that doesn't exist", testConfig{
			inputVM:  testobjects.DummyVM(nadName),
			inputVMI: testobjects.DummyVMI(nadName),
			expectedError: &errors.StatusError{
				ErrStatus: metav1.Status{
					Status:  "Failure",
					Message: "networkattachmentdefinitions.k8s.cni.cncf.io \"superdupernad\" not found",
					Reason:  "NotFound",
					Details: &metav1.StatusDetails{
						Name:  "superdupernad",
						Group: "k8s.cni.cncf.io",
						Kind:  "networkattachmentdefinitions",
					},
					Code: 404,
				},
			},
		}),
		Entry("the VMI does not exist on the datastore - it might have been deleted in the meantime", testConfig{
			expectedResponse: reconcile.Result{},
		}),
		Entry("the VMI was deleted (VM doesnt exists as well), thus IPAMClaims finalizers must be removed", testConfig{
			expectedResponse: reconcile.Result{},
			existingIPAMClaim: &ipamclaimsapi.IPAMClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:       fmt.Sprintf("%s.%s", vmName, "randomnet"),
					Namespace:  namespace,
					Finalizers: []string{claims.KubevirtVMFinalizer},
					Labels:     claims.OwnedByVMLabel(vmName),
				},
				Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
			},
			expectedIPAMClaims: []ipamclaimsapi.IPAMClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "vm1.randomnet",
						Namespace: "ns1",
						Labels:    claims.OwnedByVMLabel(vmName),
					},
					Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
				},
			},
		}),
		Entry("the VM was stopped, thus the existing IPAMClaims finalizers should be kept", testConfig{
			inputVM:          testobjects.DummyVM(nadName),
			expectedResponse: reconcile.Result{},
			existingIPAMClaim: &ipamclaimsapi.IPAMClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:       fmt.Sprintf("%s.%s", vmName, "randomnet"),
					Namespace:  namespace,
					Finalizers: []string{claims.KubevirtVMFinalizer},
					Labels:     claims.OwnedByVMLabel(vmName),
				},
				Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
			},
			expectedIPAMClaims: []ipamclaimsapi.IPAMClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "vm1.randomnet",
						Namespace:  "ns1",
						Finalizers: []string{claims.KubevirtVMFinalizer},
						Labels:     claims.OwnedByVMLabel(vmName),
					},
					Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
				},
			},
		}),
		Entry("standalone VMI, marked for deletion, without pods, thus IPAMClaims finalizers must be removed", testConfig{
			inputVMI:         dummyMarkedForDeletionVMI(nadName),
			expectedResponse: reconcile.Result{},
			existingIPAMClaim: &ipamclaimsapi.IPAMClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:       fmt.Sprintf("%s.%s", vmName, "randomnet"),
					Namespace:  namespace,
					Finalizers: []string{claims.KubevirtVMFinalizer},
					Labels:     claims.OwnedByVMLabel(vmName),
				},
				Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
			},
			expectedIPAMClaims: []ipamclaimsapi.IPAMClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "vm1.randomnet",
						Namespace: "ns1",
						Labels:    claims.OwnedByVMLabel(vmName),
					},
					Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
				},
			},
		}),
		Entry("standalone VMI which is marked for deletion, with active pods, should keep IPAMClaims finalizers", testConfig{
			inputVMI:         dummyMarkedForDeletionVMIWithActivePods(nadName),
			inputNAD:         testobjects.DummyNAD(nadName),
			expectedResponse: reconcile.Result{},
			existingIPAMClaim: &ipamclaimsapi.IPAMClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:       fmt.Sprintf("%s.%s", vmName, "randomnet"),
					Namespace:  namespace,
					Finalizers: []string{claims.KubevirtVMFinalizer},
					Labels:     claims.OwnedByVMLabel(vmName),
					OwnerReferences: []metav1.OwnerReference{{
						Name:               vmName,
						UID:                dummyUID,
						Kind:               "VirtualMachineInstance",
						Controller:         ptr.To(true),
						BlockOwnerDeletion: ptr.To(true),
					}},
				},
				Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
			},
			expectedIPAMClaims: []ipamclaimsapi.IPAMClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "vm1.randomnet",
						Namespace:  "ns1",
						Finalizers: []string{claims.KubevirtVMFinalizer},
						Labels:     claims.OwnedByVMLabel(vmName),
						OwnerReferences: []metav1.OwnerReference{{
							Name:               vmName,
							UID:                dummyUID,
							Kind:               "VirtualMachineInstance",
							Controller:         ptr.To(true),
							BlockOwnerDeletion: ptr.To(true),
						}},
					},
					Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
				},
			},
		}),
		Entry("VM which is marked for deletion, without VMI, thus IPAMClaims finalizers must be removed", testConfig{
			inputVM:          testobjects.DummyMarkedForDeletionVM(nadName),
			expectedResponse: reconcile.Result{},
			existingIPAMClaim: &ipamclaimsapi.IPAMClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:       fmt.Sprintf("%s.%s", vmName, "randomnet"),
					Namespace:  namespace,
					Finalizers: []string{claims.KubevirtVMFinalizer},
					Labels:     claims.OwnedByVMLabel(vmName),
				},
				Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
			},
			expectedIPAMClaims: []ipamclaimsapi.IPAMClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "vm1.randomnet",
						Namespace: "ns1",
						Labels:    claims.OwnedByVMLabel(vmName),
					},
					Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
				},
			},
		}),
		Entry("everything is OK but there's already an IPAMClaim with this name", testConfig{
			inputVM:  testobjects.DummyVM(nadName),
			inputVMI: testobjects.DummyVMI(nadName),
			inputNAD: testobjects.DummyNAD(nadName),
			existingIPAMClaim: &ipamclaimsapi.IPAMClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s.%s", vmName, "randomnet"),
					Namespace: namespace,
				},
				Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
			},
			expectedError: fmt.Errorf("failed since it found an existing IPAMClaim for \"vm1.randomnet\""),
		}),
		Entry("found an existing IPAMClaim for the same VM", testConfig{
			inputVM:  decorateVMWithUID(dummyUID, testobjects.DummyVM(nadName)),
			inputVMI: testobjects.DummyVMI(nadName),
			inputNAD: testobjects.DummyNAD(nadName),
			existingIPAMClaim: &ipamclaimsapi.IPAMClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s.%s", vmName, "randomnet"),
					Namespace: namespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "v1",
							Kind:       "virtualmachines",
							Name:       "vm1",
							UID:        dummyUID,
						},
					},
					Labels:     claims.OwnedByVMLabel(vmName),
					Finalizers: []string{claims.KubevirtVMFinalizer},
				},
				Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
			},
			expectedResponse: reconcile.Result{},
			expectedIPAMClaims: []ipamclaimsapi.IPAMClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "vm1.randomnet",
						Namespace: "ns1",
						Labels:    claims.OwnedByVMLabel(vmName),
						OwnerReferences: []metav1.OwnerReference{
							{
								APIVersion: "v1",
								Kind:       "virtualmachines",
								Name:       "vm1",
								UID:        dummyUID,
							},
						},
						Finalizers: []string{claims.KubevirtVMFinalizer},
					},
					Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
				},
			},
		}),
		Entry("found an existing IPAMClaim for a VM with same name but different UID", testConfig{
			inputVM:  decorateVMWithUID(dummyUID, testobjects.DummyVM(nadName)),
			inputVMI: testobjects.DummyVMI(nadName),
			inputNAD: testobjects.DummyNAD(nadName),
			existingIPAMClaim: &ipamclaimsapi.IPAMClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fmt.Sprintf("%s.%s", vmName, "randomnet"),
					Namespace: namespace,
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: "v1",
							Kind:       "virtualmachines",
							Name:       "vm1",
							UID:        unexpectedUID,
						},
					},
					Labels:     claims.OwnedByVMLabel(vmName),
					Finalizers: []string{claims.KubevirtVMFinalizer},
				},
				Spec: ipamclaimsapi.IPAMClaimSpec{Network: "doesitmatter?"},
			},
			expectedError: fmt.Errorf("failed since it found an existing IPAMClaim for \"vm1.randomnet\""),
		}),
		Entry("a lonesome VMI (with no corresponding VM) is a valid migration use-case", testConfig{
			inputVMI:         testobjects.DummyVMI(nadName),
			inputNAD:         testobjects.DummyNAD(nadName),
			expectedResponse: reconcile.Result{},
			expectedIPAMClaims: []ipamclaimsapi.IPAMClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:       "vm1.randomnet",
						Namespace:  "ns1",
						Labels:     claims.OwnedByVMLabel(vmName),
						Finalizers: []string{claims.KubevirtVMFinalizer},
						OwnerReferences: []metav1.OwnerReference{{
							Name:               vmName,
							Controller:         ptr.To(true),
							BlockOwnerDeletion: ptr.To(true)},
						},
					},
					Spec: ipamclaimsapi.IPAMClaimSpec{Network: "goodnet"},
				},
			},
		}),
	)
})

func decorateVMWithUID(uid string, vm *virtv1.VirtualMachine) *virtv1.VirtualMachine {
	vm.UID = apitypes.UID(uid)
	return vm
}

func dummyNADWrongFormat(nadName string) *nadv1.NetworkAttachmentDefinition {
	namespaceAndName := strings.Split(nadName, "/")
	return &nadv1.NetworkAttachmentDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespaceAndName[0],
			Name:      namespaceAndName[1],
		},
		Spec: nadv1.NetworkAttachmentDefinitionSpec{
			Config: "this is not JSON !!!",
		},
	}
}

func dummyMarkedForDeletionVMI(nadName string) *virtv1.VirtualMachineInstance {
	vmi := testobjects.DummyVMI(nadName)
	vmi.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	vmi.ObjectMeta.Finalizers = []string{metav1.FinalizerDeleteDependents}

	return vmi
}

func dummyMarkedForDeletionVMIWithActivePods(nadName string) *virtv1.VirtualMachineInstance {
	vmi := testobjects.DummyVMI(nadName)
	vmi.DeletionTimestamp = &metav1.Time{Time: time.Now()}
	vmi.ObjectMeta.Finalizers = []string{metav1.FinalizerDeleteDependents}

	vmi.Status.ActivePods = map[apitypes.UID]string{"podUID": "dummyNodeName"}
	vmi.UID = apitypes.UID(dummyUID)

	return vmi
}
