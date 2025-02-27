package ec2

import (
	"fmt"
	"log"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/structure"
	"github.com/hashicorp/terraform-provider-aws/internal/conns"
	"github.com/hashicorp/terraform-provider-aws/internal/flex"
	tftags "github.com/hashicorp/terraform-provider-aws/internal/tags"
)

func DataSourceVPCEndpoint() *schema.Resource {
	return &schema.Resource{
		Read: dataSourceVPCEndpointRead,

		Schema: map[string]*schema.Schema{
			"arn": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"cidr_blocks": {
				Type:     schema.TypeList,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
			},
			"dns_entry": {
				Type:     schema.TypeList,
				Computed: true,
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"dns_name": {
							Type:     schema.TypeString,
							Computed: true,
						},
						"hosted_zone_id": {
							Type:     schema.TypeString,
							Computed: true,
						},
					},
				},
			},
			"filter": CustomFiltersSchema(),
			"id": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"network_interface_ids": {
				Type:     schema.TypeSet,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Set:      schema.HashString,
			},
			"owner_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"policy": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"prefix_list_id": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"private_dns_enabled": {
				Type:     schema.TypeBool,
				Computed: true,
			},
			"requester_managed": {
				Type:     schema.TypeBool,
				Computed: true,
			},
			"route_table_ids": {
				Type:     schema.TypeSet,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Set:      schema.HashString,
			},
			"security_group_ids": {
				Type:     schema.TypeSet,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Set:      schema.HashString,
			},
			"service_name": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"state": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
			"subnet_ids": {
				Type:     schema.TypeSet,
				Computed: true,
				Elem:     &schema.Schema{Type: schema.TypeString},
				Set:      schema.HashString,
			},
			"tags": tftags.TagsSchemaComputed(),
			"vpc_endpoint_type": {
				Type:     schema.TypeString,
				Computed: true,
			},
			"vpc_id": {
				Type:     schema.TypeString,
				Optional: true,
				Computed: true,
			},
		},
	}
}

func dataSourceVPCEndpointRead(d *schema.ResourceData, meta interface{}) error {
	conn := meta.(*conns.AWSClient).EC2Conn
	ignoreTagsConfig := meta.(*conns.AWSClient).IgnoreTagsConfig

	req := &ec2.DescribeVpcEndpointsInput{}

	if id, ok := d.GetOk("id"); ok {
		req.VpcEndpointIds = aws.StringSlice([]string{id.(string)})
	}

	req.Filters = BuildAttributeFilterList(
		map[string]string{
			"vpc-endpoint-state": d.Get("state").(string),
			"vpc-id":             d.Get("vpc_id").(string),
			"service-name":       d.Get("service_name").(string),
		},
	)
	req.Filters = append(req.Filters, BuildTagFilterList(
		Tags(tftags.New(d.Get("tags").(map[string]interface{}))),
	)...)
	req.Filters = append(req.Filters, BuildCustomFilterList(
		d.Get("filter").(*schema.Set),
	)...)
	if len(req.Filters) == 0 {
		// Don't send an empty filters list; the EC2 API won't accept it.
		req.Filters = nil
	}

	log.Printf("[DEBUG] Reading VPC Endpoint: %s", req)
	respVpce, err := conn.DescribeVpcEndpoints(req)
	if err != nil {
		return fmt.Errorf("error reading VPC Endpoint: %w", err)
	}
	if respVpce == nil || len(respVpce.VpcEndpoints) == 0 {
		return fmt.Errorf("no matching VPC Endpoint found")
	}
	if len(respVpce.VpcEndpoints) > 1 {
		return fmt.Errorf("multiple VPC Endpoints matched; use additional constraints to reduce matches to a single VPC Endpoint")
	}

	vpce := respVpce.VpcEndpoints[0]
	d.SetId(aws.StringValue(vpce.VpcEndpointId))

	arn := arn.ARN{
		Partition: meta.(*conns.AWSClient).Partition,
		Service:   ec2.ServiceName,
		Region:    meta.(*conns.AWSClient).Region,
		AccountID: aws.StringValue(vpce.OwnerId),
		Resource:  fmt.Sprintf("vpc-endpoint/%s", d.Id()),
	}.String()
	d.Set("arn", arn)

	serviceName := aws.StringValue(vpce.ServiceName)
	d.Set("service_name", serviceName)
	d.Set("state", vpce.State)
	d.Set("vpc_id", vpce.VpcId)

	respPl, err := conn.DescribePrefixLists(&ec2.DescribePrefixListsInput{
		Filters: BuildAttributeFilterList(map[string]string{
			"prefix-list-name": serviceName,
		}),
	})
	if err != nil {
		return fmt.Errorf("error reading Prefix List (%s): %w", serviceName, err)
	}
	if respPl == nil || len(respPl.PrefixLists) == 0 {
		d.Set("cidr_blocks", []interface{}{})
	} else if len(respPl.PrefixLists) > 1 {
		return fmt.Errorf("multiple prefix lists associated with the service name '%s'. Unexpected", serviceName)
	} else {
		pl := respPl.PrefixLists[0]

		d.Set("prefix_list_id", pl.PrefixListId)
		err = d.Set("cidr_blocks", flex.FlattenStringList(pl.Cidrs))
		if err != nil {
			return fmt.Errorf("error setting cidr_blocks: %w", err)
		}
	}

	err = d.Set("dns_entry", flattenVPCEndpointDNSEntries(vpce.DnsEntries))
	if err != nil {
		return fmt.Errorf("error setting dns_entry: %w", err)
	}
	err = d.Set("network_interface_ids", flex.FlattenStringSet(vpce.NetworkInterfaceIds))
	if err != nil {
		return fmt.Errorf("error setting network_interface_ids: %w", err)
	}
	d.Set("owner_id", vpce.OwnerId)
	policy, err := structure.NormalizeJsonString(aws.StringValue(vpce.PolicyDocument))
	if err != nil {
		return fmt.Errorf("policy contains an invalid JSON: %w", err)
	}
	d.Set("policy", policy)
	d.Set("private_dns_enabled", vpce.PrivateDnsEnabled)
	err = d.Set("route_table_ids", flex.FlattenStringSet(vpce.RouteTableIds))
	if err != nil {
		return fmt.Errorf("error setting route_table_ids: %w", err)
	}
	d.Set("requester_managed", vpce.RequesterManaged)
	err = d.Set("security_group_ids", flattenVPCEndpointSecurityGroupIds(vpce.Groups))
	if err != nil {
		return fmt.Errorf("error setting security_group_ids: %w", err)
	}
	err = d.Set("subnet_ids", flex.FlattenStringSet(vpce.SubnetIds))
	if err != nil {
		return fmt.Errorf("error setting subnet_ids: %w", err)
	}
	err = d.Set("tags", KeyValueTags(vpce.Tags).IgnoreAWS().IgnoreConfig(ignoreTagsConfig).Map())
	if err != nil {
		return fmt.Errorf("error setting tags: %w", err)
	}
	// VPC endpoints don't have types in GovCloud, so set type to default if empty
	if vpceType := aws.StringValue(vpce.VpcEndpointType); vpceType == "" {
		d.Set("vpc_endpoint_type", ec2.VpcEndpointTypeGateway)
	} else {
		d.Set("vpc_endpoint_type", vpceType)
	}

	return nil
}
