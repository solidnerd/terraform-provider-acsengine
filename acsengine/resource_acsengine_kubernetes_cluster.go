package acsengine

// TO DO
// - add tests that check if cluster is running on nodes (I can basically only check if cluster API is there...)
// - use a CI tool in GitHub (seems to be mostly working, now I just need a successful build with acceptance tests)
// - Write documentation
// - add code coverage
// - make code more unit test-able and write more unit tests (plus clean up ones I have to use mock objects more?)
// - Important: fix dependency problems and use dep when acs-engine has been updated - DONE but update when acs-engine version has my change
// - do I need more translations?
// - get data source working (read from api model in resource state somehow)
// - OS type
// - refactor: better organization of functions, get rid of code duplication, inheritance where it makes sense, better function/variable naming
// - ask about additions to acs-engine: doesn't seem to allow tagging deployment, weird index problem

import (
	"fmt"
	"strconv"

	"github.com/Azure/acs-engine/pkg/api"
	"github.com/Azure/terraform-provider-acsengine/acsengine/helpers/kubernetes"
	"github.com/Azure/terraform-provider-acsengine/acsengine/helpers/response"
	"github.com/Azure/terraform-provider-acsengine/acsengine/utils"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/helper/validation"
)

func resourceArmAcsEngineKubernetesCluster() *schema.Resource {
	return &schema.Resource{
		Create: resourceAcsEngineK8sClusterCreate,
		Read:   resourceAcsEngineK8sClusterRead,
		Delete: resourceAcsEngineK8sClusterDelete,
		Update: resourceAcsEngineK8sClusterUpdate,
		Importer: &schema.ResourceImporter{
			State: resourceACSEngineK8sClusterImport,
		},

		Schema: map[string]*schema.Schema{
			"name": {
				Type:     schema.TypeString,
				Required: true,
				ForceNew: true,
			},

			"resource_group": resourceGroupNameSchema(),

			"kubernetes_version": kubernetesVersionSchema(),

			"location": locationSchema(),

			"linux_profile": {
				Type:     schema.TypeList,
				Required: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"admin_username": {
							Type:     schema.TypeString,
							Required: true,
						},
						"ssh": {
							Type:     schema.TypeList,
							Required: true,
							MaxItems: 1,
							Elem: &schema.Resource{
								Schema: map[string]*schema.Schema{
									"key_data": {
										Type:     schema.TypeString,
										Required: true,
									},
								},
							},
						},
					},
				},
			},

			"service_principal": {
				Type:     schema.TypeList,
				MaxItems: 1,
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"client_id": {
							Type:     schema.TypeString,
							Required: true,
						},
						"client_secret": {
							Type:      schema.TypeString,
							Required:  true,
							Sensitive: true,
						},
					},
				},
			},

			"master_profile": {
				Type:     schema.TypeList,
				Required: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"count": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      1,
							ForceNew:     true,
							ValidateFunc: validateMasterProfileCount,
						},
						"dns_name_prefix": {
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
						},
						"fqdn": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"vm_size": {
							Type:             schema.TypeString,
							Optional:         true,
							Default:          "Standard_DS1_v2",
							ForceNew:         true, // really?
							DiffSuppressFunc: ignoreCaseDiffSuppressFunc,
						},
						"os_disk_size": {
							Type:     schema.TypeInt,
							Optional: true,
							ForceNew: true,
						},
					},
				},
			},

			"agent_pool_profiles": {
				Type:     schema.TypeList, // may need to keep list sorted
				Required: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"name": {
							Type:     schema.TypeString,
							Required: true,
							ForceNew: true,
						},
						"count": {
							Type:         schema.TypeInt,
							Optional:     true,
							Default:      1,
							ValidateFunc: validateAgentPoolProfileCount,
						},
						"vm_size": {
							Type:             schema.TypeString,
							Optional:         true,
							Default:          "Standard_DS1_v2",
							ForceNew:         true,
							DiffSuppressFunc: ignoreCaseDiffSuppressFunc,
						},
						"os_disk_size": {
							Type:     schema.TypeInt,
							Optional: true,
							ForceNew: true,
						},
						"os_type": {
							Type:     schema.TypeString,
							Optional: true,
							ForceNew: true,
							Default:  api.Linux,
							ValidateFunc: validation.StringInSlice([]string{
								string(api.Linux),
								string(api.Windows),
							}, true),
							DiffSuppressFunc: ignoreCaseDiffSuppressFunc,
						},
					},
				},
			},

			"kube_config": {
				Type:     schema.TypeList,
				Computed: true,
				MaxItems: 1,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"host": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"username": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"password": {
							Type:      schema.TypeString,
							Computed:  true,
							Sensitive: true,
						},
						"client_certificate": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"client_key": {
							Type:      schema.TypeString,
							Computed:  true,
							Sensitive: true,
						},
						"cluster_ca_certificate": {
							Type:     schema.TypeString,
							Computed: true,
						},
					},
				},
			},

			"kube_config_raw": {
				Type:      schema.TypeString,
				Computed:  true,
				Sensitive: true,
			},

			"api_model": {
				Type:      schema.TypeString,
				Computed:  true,
				Sensitive: true,
			},

			"tags": tagsSchema(),
		},
	}
}

const (
	acsEngineVersion = "0.20.2" // is this completely separate from the package that calls this?
	apiVersion       = "vlabs"
)

// CRUD operations for resource

func resourceAcsEngineK8sClusterCreate(d *schema.ResourceData, m interface{}) error {
	err := createClusterResourceGroup(d, m)
	if err != nil {
		return fmt.Errorf("Failed to create resource group: %+v", err)
	}

	template, parameters, err := generateACSEngineTemplate(d, true)
	if err != nil {
		return fmt.Errorf("Failed to generate ACS Engine template: %+v", err)
	}

	id, err := deployTemplate(d, m, template, parameters)
	if err != nil {
		return fmt.Errorf("Failed to deploy template: %+v", err)
	}

	d.SetId(id)

	return resourceAcsEngineK8sClusterRead(d, m)
}

func resourceAcsEngineK8sClusterRead(d *schema.ResourceData, m interface{}) error {
	id, err := utils.ParseAzureResourceID(d.Id())
	if err != nil {
		d.SetId("")
		return err
	}
	resourceGroup := id.ResourceGroup

	if err = d.Set("resource_group", resourceGroup); err != nil {
		return fmt.Errorf("Error setting `resource_group`: %+v", err)
	}

	cluster, err := loadContainerServiceFromApimodel(d, true, false)
	if err != nil {
		return fmt.Errorf("error parsing API model: %+v", err)
	}

	err = d.Set("name", cluster.Name)
	if err != nil {
		return fmt.Errorf("error setting `name`: %+v", err)
	}
	err = d.Set("location", azureRMNormalizeLocation(cluster.Location))
	if err != nil {
		return fmt.Errorf("error setting `location`: %+v", err)
	}
	err = d.Set("kubernetes_version", cluster.Properties.OrchestratorProfile.OrchestratorVersion)
	if err != nil {
		return fmt.Errorf("error setting `kubernetes_version`: %+v", err)
	}

	if err = setProfiles(d, cluster); err != nil {
		return err
	}

	if err = setTags(d, cluster.Tags); err != nil {
		return err
	}

	if err = setKubeConfig(d, cluster); err != nil {
		return err
	}

	// set apimodel here? doesn't really make sense if I'm using that to set everything

	fmt.Println("finished reading")

	return nil
}

func resourceAcsEngineK8sClusterDelete(d *schema.ResourceData, m interface{}) error {
	client := m.(*ArmClient)
	rgClient := client.resourceGroupsClient
	ctx := client.StopContext

	id, err := utils.ParseAzureResourceID(d.Id())
	if err != nil {
		return fmt.Errorf("Error parsing Azure Resource ID %q: %+v", d.Id(), err)
	}

	resourceGroupName := id.ResourceGroup

	deleteFuture, err := rgClient.Delete(ctx, resourceGroupName)
	if err != nil {
		if response.WasNotFound(deleteFuture.Response()) {
			return nil
		}

		return fmt.Errorf("Error deleting Resource Group %q: %+v", resourceGroupName, err)
	}

	err = deleteFuture.WaitForCompletion(ctx, rgClient.Client)
	if err != nil {
		if response.WasNotFound(deleteFuture.Response()) {
			return nil
		}

		return fmt.Errorf("Error deleting Resource Group %q: %+v", resourceGroupName, err)
	}

	return nil
}

func resourceAcsEngineK8sClusterUpdate(d *schema.ResourceData, m interface{}) error {
	_, err := utils.ParseAzureResourceID(d.Id())
	if err != nil {
		d.SetId("")
		return err
	}

	d.Partial(true)

	// UPGRADE
	if d.HasChange("kubernetes_version") {
		old, new := d.GetChange("kubernetes_version")
		if err = kubernetes.ValidateKubernetesVersionUpgrade(new.(string), old.(string)); err != nil {
			return fmt.Errorf("Error upgrading Kubernetes version: %+v", err)
		}
		if err = upgradeCluster(d, m, new.(string)); err != nil {
			return fmt.Errorf("Error upgrading Kubernetes version: %+v", err)
		}

		d.SetPartial("kubernetes_version")
	}

	// SCALE
	agentPoolProfiles := d.Get("agent_pool_profiles").([]interface{})
	for i := 0; i < len(agentPoolProfiles); i++ {
		profileCount := "agent_pool_profiles." + strconv.Itoa(i) + ".count"
		if d.HasChange(profileCount) {
			count := d.Get(profileCount).(int)
			if err = scaleCluster(d, m, i, count); err != nil {
				return fmt.Errorf("Error scaling agent pool: %+v", err)
			}
		}

		d.SetPartial(profileCount)
	}

	if d.HasChange("tags") {
		if err = updateResourceGroupTags(d, m); err != nil {
			return fmt.Errorf("Error updating tags: %+v", err)
		}

		d.SetPartial("tags")
	}

	d.Partial(false)

	return resourceAcsEngineK8sClusterRead(d, m)
}

// HELPER FUNCTIONS

/* 'Read' Helper Functions */

func setProfiles(d *schema.ResourceData, cluster *api.ContainerService) error {
	linuxProfile, err := flattenLinuxProfile(*cluster.Properties.LinuxProfile)
	if err != nil {
		return fmt.Errorf("Error flattening `linux_profile`: %+v", err)
	}
	if err = d.Set("linux_profile", linuxProfile); err != nil {
		return fmt.Errorf("Error setting 'linux_profile': %+v", err)
	}

	servicePrincipal, err := flattenServicePrincipal(*cluster.Properties.ServicePrincipalProfile)
	if err != nil {
		return fmt.Errorf("Error flattening `service_principal`: %+v", err)
	}
	if err = d.Set("service_principal", servicePrincipal); err != nil {
		return fmt.Errorf("Error setting 'service_principal': %+v", err)
	}

	masterProfile, err := flattenMasterProfile(*cluster.Properties.MasterProfile, cluster.Location)
	if err != nil {
		return fmt.Errorf("Error flattening `master_profile`: %+v", err)
	}
	if err = d.Set("master_profile", masterProfile); err != nil {
		return fmt.Errorf("Error setting 'master_profile': %+v", err)
	}

	agentPoolProfiles, err := flattenAgentPoolProfiles(cluster.Properties.AgentPoolProfiles)
	if err != nil {
		return fmt.Errorf("Error flattening `agent_pool_profiles`: %+v", err)
	}
	if err = d.Set("agent_pool_profiles", agentPoolProfiles); err != nil {
		return fmt.Errorf("Error setting 'agent_pool_profiles': %+v", err)
	}

	return nil
}
