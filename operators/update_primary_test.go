package operators

import (
	"context"

	"github.com/cybozu-go/moco/accessor"
	"github.com/cybozu-go/moco/api/v1alpha1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("Update primary", func() {

	ctx := context.Background()

	BeforeEach(func() {
		err := startMySQLD(mysqldName1, mysqldPort1, mysqldServerID1)
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
	})

	AfterEach(func() {
		stopMySQLD(mysqldName1)
		stopMySQLD(mysqldName2)
	})

	logger := ctrl.Log.WithName("operators-test")

	It("should update primary", func() {
		_, infra, cluster := getAccessorInfraCluster()
		cluster.Spec.Replicas = 3
		_, err := ctrl.CreateOrUpdate(ctx, k8sClient, &cluster, func() error {
			return nil
		})
		Expect(err).ShouldNot(HaveOccurred())

		db, err := infra.GetDB(0)
		Expect(err).ShouldNot(HaveOccurred())
		_, err = db.Exec(`CHANGE MASTER TO MASTER_HOST = ?, MASTER_PORT = ?, MASTER_USER = ?, MASTER_PASSWORD = ?`, mysqldName2, mysqldPort2, userName, password)
		Expect(err).ShouldNot(HaveOccurred())
		_, err = db.Exec(`START SLAVE`)
		Expect(err).ShouldNot(HaveOccurred())

		op := updatePrimaryOp{
			newPrimaryIndex: 1,
		}

		status := accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)

		err = op.Run(ctx, infra, &cluster, status)
		Expect(err).ShouldNot(HaveOccurred())

		updateCluster := v1alpha1.MySQLCluster{}
		err = k8sClient.Get(ctx, client.ObjectKey{Namespace: cluster.Namespace, Name: cluster.Name}, &updateCluster)
		Expect(err).ShouldNot(HaveOccurred())

		Expect(updateCluster.Status.CurrentPrimaryIndex).ShouldNot(BeNil())
		Expect(*updateCluster.Status.CurrentPrimaryIndex).Should(Equal(1))

		Expect(*cluster.Status.CurrentPrimaryIndex).Should(Equal(1))
		status = accessor.GetMySQLClusterStatus(ctx, logger, infra, &cluster)
		Expect(status.InstanceStatus).Should(HaveLen(3))

		primaryStatus := status.InstanceStatus[1]
		Expect(primaryStatus.ReplicaStatus).Should(BeNil())
		Expect(primaryStatus.GlobalVariablesStatus.RplSemiSyncMasterWaitForSlaveCount).Should(Equal(1))
	})
})