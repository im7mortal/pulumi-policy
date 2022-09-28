# Copyright 2016-2018, Pulumi Corporation.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""
The Pulumi Policy SDK for Python.
"""

# Make all module members inside of this package available as package members.
from .policy import (
    EnforcementLevel,
    Policy,
    PolicyConfigSchema,
    PolicyCustomTimeouts,
    PolicyPack,
    PolicyProviderResource,
    PolicyResource,
    PolicyResourceOptions,
    ReportViolation,
    ResourceValidation,
    ResourceValidationArgs,
    ResourceValidationPolicy,
    StackValidation,
    StackValidationArgs,
    StackValidationPolicy,
)

import pulumi.runtime
import os

# If any config variables are present, parse and set them, so subsequent accesses are fast.
config_env = pulumi.runtime.get_config_env()
for k, v in config_env.items():
    pulumi.runtime.set_config(k, v)

# Configure the runtime so that the user program hooks up to Pulumi as appropriate.
if (
    "PULUMI_PROJECT" in os.environ
    and "PULUMI_STACK" in os.environ
    and "PULUMI_DRY_RUN" in os.environ
):
    pulumi.runtime.configure(
        pulumi.runtime.Settings(
            project=os.environ["PULUMI_PROJECT"],
            stack=os.environ["PULUMI_STACK"],
            dry_run=os.environ["PULUMI_DRY_RUN"] == "true",
            # PULUMI_ORGANIZATION might not be set for filestate backends
            organization=os.environ.get("PULUMI_ORGANIZATION", "organization"),
        )
    )