# Revision Controller

## Overview

Revision Controller is a tool that trigger build when the source code changed, or rerun the application when the image changed.

- Watch the source code in github, gitlab or gitee, rebuild the function when source code changed.
- Watch the bundle container image which include the soucre code, rebuild the function when image changed.
- Watch the images of functions that without builder, rerun the image when image changed.

## Quick start

### Install

We assume you already have a Kubernetes cluster (v1.20+) with `OpenFunction`. If you don't, you can follow [this](https://openfunction.dev/docs/getting-started/installation/#install-openfunction) to install `OpenFunction`.

Exec the following commands to deploy the Revision Controller.

```shell
kubectl apply -f deploy/bundle.yaml
```

### How to use

To watch a function, just need to add some annotations to it. 

```yaml
apiVersion: core.openfunction.io/v1beta1
kind: Function
metadata:
  annotations:
    openfunction.io/revision-controller: enable
    openfunction.io/revision-controller-params: |
      type: source
      repo-type: github
      polling-interval: 1m
  name: function-http-java
  namespace: default
spec:
  build:
    ...
  serving:
    ...
```

Annotations

| Key                                            | Description                                                                                    |
| ---------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| **openfunction.io/revision-controller**        | Whether to start a revision controller for this function, known values are enable and disable. |
| **openfunction.io/revision-controller-params** | Parameters for revision controller.                                                            |

Parameters

| Name                  | Description                                                                                                        |
| --------------------- | ------------------------------------------------------------------------------------------------------------------ |
| **type**              | The target type to watch, known values are source, source-image, image.                                            |
| **polling-interval**  | The interval to get the image digest or source code head.                                                          |
| **repo-type**         | The type of the git server where the source code be in, known values are github, gitlab, gitee, default is github. |
| **base-url**          | The base url of the gitlab server.                                                                                 |
| **auth-type**         | The auth type of the gitlab server.                                                                                |
| **project-id**        | The project id of a gitlab repo.                                                                                   |
| **insecure-registry** | If the image registy is insecure, you should set this to true.                                                     |
