#!/bin/bash -e
#
# This file is part of the KubeVirt project
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
#
# Copyright 2024 Red Hat, Inc.
#

export KUBEVIRT_COMMIT=01c651ba0677f759220ce0fa43530477bbe8ee23

KUBEVIRT_REPO='https://github.com/kubevirt/kubevirt.git'
REPO_PATH=${REPO_PATH:-"passt/_kubevirt"}

function cluster::_get_repo() {
    git --git-dir ${REPO_PATH}/.git config --get remote.origin.url
}

function cluster::_get_sha() {
    git --git-dir ${REPO_PATH}/.git rev-parse HEAD
}

function cluster::install() {
    if [ -d ${REPO_PATH} ]; then
        if [ $(cluster::_get_repo) != ${KUBEVIRT_REPO} -o $(cluster::_get_sha) != ${KUBEVIRT_COMMIT} ]; then
            rm -rf ${REPO_PATH}
        fi
    fi

    if [ ! -d ${REPO_PATH} ]; then
        git clone ${KUBEVIRT_REPO} ${REPO_PATH}
        (
            cd ${REPO_PATH}
            git checkout ${KUBEVIRT_COMMIT}
        )
    fi
}

cluster::install
