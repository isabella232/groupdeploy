groupdeploy
===========

deploy software via Google instance groups

    (export GOOGLE_APPLICATION_CREDENTIALS="$YOUR_CREDENTIALS_JSON_FILE" ; ./groupdeploy -group  INSTANCE_GROUP -image IMAGE_NAME  -project PROJECT_NAME -template INSTANCE_TEMPLATE )


it makes a bit of assumptions about its parameters:

 * `IMAGE_NAME` should have the git revision as the last name component
    e.g. common-0000000000000000000000000000000000000000
 * `INSTANCE_TEMPLATE` should have `defaults` as the last name component
    e.g. templatename-defaults


The software will create a new intance template based on the given instance
template and replace the `defaults` with the hash. It also sets the item key
`app_version` to the git revision extracted from the supplied image name

Then it will update the supplied instance group with the new instance template
and force re-creation of all instances.
