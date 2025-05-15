# Replicated MPC Nodes with Broker Communication using local Kind cluster

This directory has an example of deploying Replicated MPC Nodes with Message Broker Communication locally to your desktop for helm testing and development. When deploying to production, deploy each TSM node to a separate Kubernetes cluster. For more information on the Broker Communication, see the [Broker Communication](https://builder-vault-tsm.docs.blockdaemon.com/docs/message-broker-communication) and [Horizontal Scaling](https://builder-vault-tsm.docs.blockdaemon.com/docs/horizontal-scaling#replicated-mpc-nodes-with-message-broker-communication) documentation.

## Helm Repository

```shell
helm repo add builder-vault https://blockdaemon.github.io/builder-vault-helm/
helm repo update
```

## Prerequisites

Each Builder Vault TSM node must have it own configuration which it reads on startup from `/config/config.toml`. A sample config file for each node is provided: `config0.toml`, `config1.toml`, `config2.toml`. Sensitive values are interpolated using environment variables. When not testing locally, these must be retrieved from your secrets management infrastructure.

The deployed cluster will look like this:

```mermaid
C4Deployment
    title Builder Vault TSM Deployment with Message Broker

    Person(sdk, "SDK", "Client application using Builder Vault SDK")

    Deployment_Node(k8s1, "Kubernetes Cluster 1", "Kubernetes") {
        Deployment_Node(ns1, "TSM Node 1 Namespace", "Kubernetes Namespace") {
            Deployment_Node(lb1, "Load Balancers", "Kubernetes Service") {
                Container(sdk_lb1, "SDK Load Balancer", "Load balances SDK requests")
            }
            Deployment_Node(instances1, "Instances", "Kubernetes Deployment") {
                Container(instance1a, "Instance A", "TSM Node 1 Instance A")
                Container(instance1b, "Instance B", "TSM Node 1 Instance B")
            }
            Deployment_Node(db1, "Node 1 Database", "External Database") {
                ContainerDb(db1, "Database", "Node 1 Database")
            }
        }
    }

    Deployment_Node(k8s2, "Kubernetes Cluster 2", "Kubernetes") {
        Deployment_Node(ns2, "TSM Node 2 Namespace", "Kubernetes Namespace") {
            Deployment_Node(lb2, "Load Balancers", "Kubernetes Service") {
                Container(sdk_lb2, "SDK Load Balancer", "Load balances SDK requests")
            }
            Deployment_Node(instances2, "Instances", "Kubernetes Deployment") {
                Container(instance2a, "Instance A", "TSM Node 2 Instance A")
                Container(instance2b, "Instance B", "TSM Node 2 Instance B")
            }
            Deployment_Node(db2, "Node 2 Database", "External Database") {
                ContainerDb(db2, "Database", "Node 2 Database")
            }
        }
    }

    Deployment_Node(k8s3, "Kubernetes Cluster 3", "Kubernetes") {
        Deployment_Node(ns3, "TSM Node 3 Namespace", "Kubernetes Namespace") {
            Deployment_Node(lb3, "Load Balancers", "Kubernetes Service") {
                Container(sdk_lb3, "SDK Load Balancer", "Load balances SDK requests")
            }
            Deployment_Node(instances3, "Instances", "Kubernetes Deployment") {
                Container(instance3a, "Instance A", "TSM Node 3 Instance A")
                Container(instance3b, "Instance B", "TSM Node 3 Instance B")
            }
            Deployment_Node(db3, "Node 3 Database", "External Database") {
                ContainerDb(db3, "Database", "Node 3 Database")
            }
        }
    }

    Deployment_Node(broker, "Message Broker", "External Service") {
        Container(broker, "Message Broker", "Handles inter-node communication")
    }

    Rel(sdk, sdk_lb1, "Uses", "HTTP/HTTPS")
    Rel(sdk, sdk_lb2, "Uses", "HTTP/HTTPS")
    Rel(sdk, sdk_lb3, "Uses", "HTTP/HTTPS")

    Rel(sdk_lb1, instance1a, "Routes to", "HTTP/HTTPS")
    Rel(sdk_lb1, instance1b, "Routes to", "HTTP/HTTPS")
    Rel(sdk_lb2, instance2a, "Routes to", "HTTP/HTTPS")
    Rel(sdk_lb2, instance2b, "Routes to", "HTTP/HTTPS")
    Rel(sdk_lb3, instance3a, "Routes to", "HTTP/HTTPS")
    Rel(sdk_lb3, instance3b, "Routes to", "HTTP/HTTPS")

    Rel(instance1a, db1, "Reads/Writes", "Database Protocol")
    Rel(instance1b, db1, "Reads/Writes", "Database Protocol")
    Rel(instance2a, db2, "Reads/Writes", "Database Protocol")
    Rel(instance2b, db2, "Reads/Writes", "Database Protocol")
    Rel(instance3a, db3, "Reads/Writes", "Database Protocol")
    Rel(instance3b, db3, "Reads/Writes", "Database Protocol")

    Rel(instance1a, broker, "Publishes/Subscribes", "Message Protocol")
    Rel(instance1b, broker, "Publishes/Subscribes", "Message Protocol")
    Rel(instance2a, broker, "Publishes/Subscribes", "Message Protocol")
    Rel(instance2b, broker, "Publishes/Subscribes", "Message Protocol")
    Rel(instance3a, broker, "Publishes/Subscribes", "Message Protocol")
    Rel(instance3b, broker, "Publishes/Subscribes", "Message Protocol")
```

## Deployment

### Single node Kind cluster deployment
```shell
cat <<EOF | kind create cluster --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
  extraPortMappings:
  - containerPort: 80
    hostPort: 80
    protocol: TCP
EOF
```

### Nginx ingress deployment
```shell
kubectl apply -f https://kind.sigs.k8s.io/examples/ingress/deploy-ingress-nginx.yaml
```

### Deploy databases:
```shell
kubectl create namespace tsm
kubectl apply -n tsm -f db0-postgres.yaml
kubectl apply -n tsm -f db1-mysql.yaml
kubectl apply -n tsm -f db2-postgres.yaml
```

### Deploy broker:
```shell
kubectl apply -n tsm -f broker-redis.yaml
```

### Deploy each BuilderVault TSM node:
  - update tsm[0-2].yaml `image.repository` with your container registery. 
```shell
kind load docker-image <registry>/tsm-node:69.0.0
helm install tsm0 builder-vault/tsm-node --create-namespace -n tsm -f tsm0.yaml
helm install tsm1 builder-vault/tsm-node --create-namespace -n tsm -f tsm1.yaml
helm install tsm2 builder-vault/tsm-node --create-namespace -n tsm -f tsm2.yaml
```

### Benchmark
```shell
cd ../../benchmark
go run . -operation sign -ecdsaClients 25 -duration 30s -threshold 1 -signers 3 -node http://apikey0@localhost:80/tsm0 -node http://apikey1@localhost:80/tsm1 -node http://apikey2@localhost:80/tsm2
```