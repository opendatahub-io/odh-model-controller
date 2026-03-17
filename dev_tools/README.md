# Using Devspace

[Devspace site](https://devspace.sh)

## Prerequisites to use devspace

**These get copied into the running container and are defined in the devspace.yaml file.**

1. Binaries
   - devspace: [Devspace install instructions](https://www.devspace.sh/docs/getting-started/installation)
   - kubectl
   - helm
   - git
2. kubeconfig in `~/.kube/config` with current context set to cluster you want to use

## Using Devspace

### If no devspace.yaml or devspace.sh files exist you will need to generate them

**Note: This has already been run in this repo and should not be needed.**

- Run `devspace init`

This will also add .devspace to your .gitignore file. If it doesn't, please add it.

#### The devspace.yaml file (config file) defines the following:

[Devspace Configuration](https://www.devspace.sh/docs/configuration/reference)

- Container Image that will be used when creating the devspace container
- The labelselector that will be used to choose which deployment it will be replacing
- The binaries that it will copy into the container
- The available pipelines a user can run `devspace run-pipeline ${pipeline_name}`
- ENV Vars that can be defined under the **vars** section
- The path to the manifest and what tool to use to deploy (helm or kustomize)
  - in this repos case `../config/manager` is the path `kustomize` is being used

### Common commands

[More Development Commands](https://www.devspace.sh/docs/getting-started/development)

**NOTE:** You don't have to run the use context and use namespace commands if you haven't misconfigured devspace since the last time you used it with this repo.

For regular development you usually run `devspace use context`, then `devspace use namespace`, then `devspace dev`

- `devspace use context` to select your kubernetes cluster in the case you have multiple in your kubeconfig file
- `devspace use namespace` change what namepsace you want to deploy your container into e.g. `devspace use namespace opendatahub` for this project
- `devspace dev` main command used to start up the devspace container
- `devspace run-pipeline ${pipeline_name}` to run a specific pipeline e.g. `debug` pipeline that was configured for this project

[More Cleanup Commands](https://www.devspace.sh/docs/getting-started/cleanup)

- `devspace purge` used to delete your project from the cluster
- `devspace reset pods` to reverse `start_dev` command that devspace runes within a pipeline
