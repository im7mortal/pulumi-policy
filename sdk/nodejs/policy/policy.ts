// Copyright 2016-2019, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

import { Resource } from "@pulumi/pulumi";
import * as q from "@pulumi/pulumi/queryable";
import { serve } from "./server";

export interface PolicyPackArgs {
    policies: Policies;
}

export class PolicyPack {
    private readonly policies: Policies;

    constructor(private name: string, args: PolicyPackArgs) {
        this.policies = args.policies;

        //
        // TODO: Wire up version information obtained from the service.
        //
        const version = "1";

        serve(this.name, version, this.policies);
    }
}

/** A helper function that returns a strongly typed resource validation function. */
export function typedResourceValidation<TResource extends Resource>(
    filter: (o: any) => o is TResource,
    validate: (args: ResourceValidationArgs<q.ResolvedResource<TResource>>, reportViolation: ReportViolation) => Promise<void> | void,
): ResourceValidation {
    return (args: ResourceValidationArgs<any>, reportViolation: ReportViolation) => {
        args.props.__pulumiType = args.type;
        if (filter(args.props) === false) {
            return;
        }
        return validate(args, reportViolation);
    };
}

/**
 * Indicates the impact of a policy violation.
 */
export type EnforcementLevel = "advisory" | "mandatory";

/**
 * A policy function that returns true if a resource definition violates some policy (e.g., "no
 * public S3 buckets"), and a set of metadata useful for generating helpful messages when the policy
 * is violated.
 */
export interface Policy {
    /** An ID for the policy. Must be unique to the current policy set. */
    name: string;

    /**
     * A brief description of the policy rule. e.g., "S3 buckets should have default encryption
     * enabled."
     */
    description: string;

    /**
     * Indicates what to do on policy violation, e.g., block deployment but allow override with
     * proper permissions.
     */
    enforcementLevel: EnforcementLevel;
}

export type Policies = (ResourceValidationPolicy | StackValidationPolicy)[];

export interface ResourceValidationPolicy extends Policy {
    validateResource: ResourceValidation;
}

export type ResourceValidation = (args: ResourceValidationArgs<any>, reportViolation: ReportViolation) => Promise<void> | void;

export interface ResourceValidationArgs<TResource> {
    type: string;
    props: TResource;
}

export interface StackValidationPolicy extends Policy {
    validateStack: StackValidation;
}

export type StackValidation = (args: StackValidationArgs, reportViolation: ReportViolation) => Promise<void> | void;

export interface StackValidationArgs {
    resources: PolicyResource[];
}

export interface PolicyResource {
    type: string;
    props: Record<string, any>;
}

export interface PolicyViolation {
    message: string;
    urn?: string;
}

export type ReportViolation = (violation: string | PolicyViolation) => void;
