# KubeDrainRollout

This project is a workaround for https://github.com/kubernetes/kubernetes/issues/48307

TL;DR, Kubernetes currently does not correctly drain a node when a PodDisruptionBudget has MinAvailable = 1 and a Deployment has Replicas = 1, it will get stuck indefinitely.

This project essentially triggers `kubectl rollout redeploy` on deployments in this scenario when the node is unschedulable in order to gracefully re-schedule the pods using the deployment strategy, which allows the replicas to temporarily surge if configured.

It is currently unfinished and does not yet work as intended. Do not use this project yet.
