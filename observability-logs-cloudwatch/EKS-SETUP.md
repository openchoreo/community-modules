1. Create a role with the required policies.

trusted entities
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Principal": {
                "Service": "pods.eks.amazonaws.com"
            },
            "Action": [
                "sts:AssumeRole",
                "sts:TagSession"
            ]
        }
    ]
}

policies same as the normal external server (k3d/kind/etc)

2. Attach role to the Create Pod Identity association in the EKS Resource.

Configurations in aws console:
"Create Pod Identity association Info
Map an IAM role to a Kubernetes service account.

Introducing Pod Identity to access AWS resources in another AWS account
You can now easily create a Pod Identity association to access AWS resources in another account. Simply create an IAM role in each of the accounts with the necessary trust and permission policies, then specify the roles in the 'IAM role' and 'Target IAM role' fields on this page. EKS will then perform role chaining to ensure your application receives these permissions.
Pod Identity configuration
IAM role
Choose an existing role for the workload to assume. The list only shows roles with a trust policy for Pod Identity.

openchoreo-cloudwatch-eks-role

Create recommended role
Target IAM role - optional, new
Info
Specify a target IAM role that will be assumed by the IAM role you specified above, allowing your workloads to inherit the permissions attached to the target IAM role.

Create recommended role
Must be written as arn:partition:service::account-id:role/role-name
Kubernetes namespace
Select an existing namespace or enter the name of a namespace for your workload.
openchoreo-observability-plane


Kubernetes service account
Select an existing service account or enter the name of a new service account so that the IAM role will be associated with it.

Session tags
Info
Disable session tags
Disable the default session tags that EKS Pod Identity automatically adds when it assumes the role.
Session policy configuration
Configure an IAM policy to apply additional restrictions to this pod identity association beyond the IAM policies attached to the IAM role.
Configure session policy"



Gemini model output for this:
"You are in the exact right place! Because EKS Pod Identity allows you to pre-map these connections even before you install your Helm chart, you can just type the names directly into the fields.

Here is exactly how to fill out this form for your Adapter:

1. Pod Identity configuration
IAM role: openchoreo-cloudwatch-eks-role (Leave this exactly as you have it).

Target IAM role: Leave this blank. (This is a brand new AWS feature for cross-account setups, which you do not need).

2. Kubernetes configuration
Kubernetes namespace: Type openchoreo-observability-plane

Kubernetes service account: Type observability-logs-cloudwatch-adapter

3. Session tags & Session policy
Disable session tags: Leave this unchecked.

Configure session policy: Leave this blank.

Tags: Leave this blank (unless your organization requires billing tags).

Click Create!

Important: You need to do this twice
Because your module has two different components running in the cluster (the Go Adapter reading the logs, and Fluent Bit writing the logs), they use different Service Accounts.

After you click Create, click "Create Pod Identity association" again to make a second one for Fluent Bit:

IAM role: openchoreo-cloudwatch-eks-role (You can safely reuse the same role).

Kubernetes namespace: openchoreo-observability-plane

Kubernetes service account: Type the Fluent Bit service account name. Because this comes from the upstream AWS subchart, its name depends on your Helm release name. It will likely be observability-logs-cloudwatch-fluent-bit or amazon-cloudwatch-observability-fluent-bit.

Pro-tip: If you aren't 100% sure what the Fluent Bit service account will be named, you can run your Helm install command first, then run this command in your terminal to see the exact Service Account names it created:

Bash

kubectl get sa -n openchoreo-observability-plane
Once you see the Fluent Bit service account name in that list, come back to this screen and create the second association for it."