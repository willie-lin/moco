package operators

import (
	"context"
	"strconv"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/accessor"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

var replicationSource = "replication-source"

var _ = Describe("Configure intermediate primary operator", func() {

	ctx := context.Background()

	BeforeEach(func() {
		err := createNetwork()
		Expect(err).ShouldNot(HaveOccurred())

		err = startMySQLD(mysqldName1, mysqldPort1, mysqldServerID1)
		Expect(err).ShouldNot(HaveOccurred())
		err = startMySQLD(mysqldName2, mysqldPort2, mysqldServerID2)
		Expect(err).ShouldNot(HaveOccurred())

		err = initializeMySQL(mysqldPort1)
		Expect(err).ShouldNot(HaveOccurred())
		err = initializeMySQL(mysqldPort2)
		Expect(err).ShouldNot(HaveOccurred())

		ns := corev1.Namespace{}
		ns.Name = namespace
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &ns, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		secret := corev1.Secret{}
		secret.Namespace = namespace
		secret.Name = replicationSource
		_, err = ctrl.CreateOrUpdate(ctx, k8sClient, &secret, func() error {
			secret.Data = map[string][]byte{
				"PRIMARY_HOST":     []byte(mysqldName2),
				"PRIMARY_PORT":     []byte(strconv.Itoa(mysqldPort2)),
				"PRIMARY_USER":     []byte(userName),
				"PRIMARY_PASSWORD": []byte(password),
			}
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())
	})

	AfterEach(func() {
		stopMySQLD(mysqldName1)
		stopMySQLD(mysqldName2)
		removeNetwork()

		ns := corev1.Namespace{}
		ns.Name = namespace
		k8sClient.Delete(ctx, &ns)
	})

	logger := ctrl.Log.WithName("operators-test")

	It("should be intermediate primary", func() {
		_, infra, cluster := getAccessorInfraCluster()
		cluster.Spec.ReplicationSourceSecretName = &replicationSource

		op := configureIntermediatePrimaryOp{
			Index: 0,
			Options: &accessor.IntermediatePrimaryOptions{
				PrimaryHost:     mysqldName2,
				PrimaryUser:     userName,
				PrimaryPassword: password,
				PrimaryPort:     mysqldPort2,
			},
		}

		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		status := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(status.InstanceStatus).Should(HaveLen(2))
		Expect(status.InstanceStatus[0].GlobalVariablesStatus.ReadOnly).Should(BeTrue())
		replicaStatus := status.InstanceStatus[0].ReplicaStatus
		Expect(replicaStatus).ShouldNot(BeNil())
		Expect(replicaStatus.MasterHost).Should(Equal(mysqldName2))
		Expect(replicaStatus.LastIoErrno).Should(Equal(0))
		Expect(replicaStatus.SlaveIORunning).Should(Equal(moco.ReplicaRunConnect))
		Expect(replicaStatus.SlaveSQLRunning).Should(Equal(moco.ReplicaRunConnect))
	})

	It("should do nothing when options is empty", func() {
		_, infra, cluster := getAccessorInfraCluster()
		cluster.Spec.ReplicationSourceSecretName = &replicationSource

		op := configureIntermediatePrimaryOp{
			Index:   0,
			Options: nil,
		}

		err := op.Run(ctx, infra, &cluster, nil)
		Expect(err).ShouldNot(HaveOccurred())

		status := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(status.InstanceStatus).Should(HaveLen(2))
		Expect(status.InstanceStatus[0].GlobalVariablesStatus.ReadOnly).Should(BeFalse())
		Expect(status.InstanceStatus[0].ReplicaStatus).Should(BeNil())
	})
})
