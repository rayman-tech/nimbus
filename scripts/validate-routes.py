#!/usr/bin/env python3
"""Validate rendered routing fixtures against pinned CRDs, without cluster access."""
import argparse
import base64
import subprocess
import tempfile
import copy
import json
from pathlib import Path
import yaml
import jsonschema


def strict(schema):
    schema = copy.deepcopy(schema)
    if isinstance(schema, dict):
        if schema.get('type') == 'object' and 'properties' in schema and not schema.get('x-kubernetes-preserve-unknown-fields'):
            schema.setdefault('additionalProperties', False)
        return {k: strict(v) for k, v in schema.items()}
    if isinstance(schema, list):
        return [strict(v) for v in schema]
    return schema


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('fixtures', type=Path)
parser.add_argument('crds', type=Path)
parser.add_argument('--egctl', help='Optional pinned egctl executable for offline xDS translation')
args = parser.parse_args()
schemas = {}
for d in yaml.safe_load_all(args.crds.read_text()):
    if not d or d.get('kind') != 'CustomResourceDefinition':
        continue
    for v in d['spec']['versions']:
        if v['served']:
            s = strict(v['schema']['openAPIV3Schema'])
            s['properties']['metadata'] = {'type': 'object'}
            schemas[(d['spec']['group'] + '/' + v['name'], d['spec']['names']['kind'])] = s
checked = 0
for d in json.loads(args.fixtures.read_text()):
    key = d['apiVersion'], d['kind']
    if key not in schemas:
        assert d['apiVersion'] in ['v1', 'apps/v1', 'cert-manager.io/v1'], key
        continue
    jsonschema.Draft7Validator(schemas[key]).validate(d)
    checked += 1
print(f'{checked} Gateway API / Envoy objects passed strict CRD schema validation.')

if args.egctl:
    original = json.loads(args.fixtures.read_text())
    fixtures = [copy.deepcopy(d) for d in original if d['kind'] not in ['Certificate', 'Deployment', 'Gateway']]
    listeners = [{'name': 'http', 'protocol': 'HTTP', 'port': 80, 'allowedRoutes': {'namespaces': {'from': 'All'}}}]
    for d in original:
        if d['kind'] == 'Gateway':
            listeners.extend(copy.deepcopy(d['spec']['listeners']))
    by_name = {l['name']: l for l in listeners}
    # egctl 1.9 discards Namespace labels. Assert real selectors, then widen
    # only the throwaway fixture so routes can be translated offline.
    for d in fixtures:
        if d['kind'] in ['HTTPRoute', 'GRPCRoute']:
            for ref in d['spec']['parentRefs']:
                allowed = by_name[ref['sectionName']]['allowedRoutes']['namespaces']
                if allowed['from'] == 'Selector':
                    assert allowed['selector']['matchLabels'] == {'kubernetes.io/metadata.name': d['metadata']['namespace']}
    for listener in listeners:
        listener['allowedRoutes']['namespaces'] = {'from': 'All'}
    fixtures.extend([
        {'apiVersion': 'gateway.networking.k8s.io/v1', 'kind': 'GatewayClass', 'metadata': {'name': 'edge'}, 'spec': {'controllerName': 'gateway.envoyproxy.io/gatewayclass-controller'}},
        {'apiVersion': 'gateway.networking.k8s.io/v1', 'kind': 'Gateway', 'metadata': {'name': 'edge', 'namespace': 'envoy-gateway-system'}, 'spec': {'gatewayClassName': 'edge', 'listeners': listeners}},
        {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': 'fixture'}},
        {'apiVersion': 'v1', 'kind': 'Namespace', 'metadata': {'name': 'envoy-gateway-system'}},
    ])
    known = {d['metadata']['name'] for d in fixtures if d['kind'] == 'Service'}
    for d in list(fixtures):
        if d['kind'] not in ['HTTPRoute', 'GRPCRoute']:
            continue
        for rule in d['spec']['rules']:
            for ref in rule.get('backendRefs', []):
                if ref['name'] in known:
                    continue
                port = {'port': ref['port']}
                if d['kind'] == 'GRPCRoute':
                    port['appProtocol'] = 'kubernetes.io/h2c'
                fixtures.append({'apiVersion': 'v1', 'kind': 'Service', 'metadata': {'name': ref['name'], 'namespace': 'fixture'}, 'spec': {'ports': [port], 'clusterIP': '10.254.0.1'}})
                known.add(ref['name'])
    for d in fixtures:
        if d['kind'] == 'Service':
            d['spec'].setdefault('clusterIP', '10.254.0.2')
    with tempfile.TemporaryDirectory() as directory:
        tmp = Path(directory)
        subprocess.run(['openssl', 'req', '-x509', '-newkey', 'rsa:2048', '-nodes', '-keyout', str(tmp/'key.pem'), '-out', str(tmp/'cert.pem'), '-days', '1', '-subj', '/CN=offline.invalid'], check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        for listener in listeners:
            for ref in listener.get('tls', {}).get('certificateRefs', []):
                fixtures.append({'apiVersion': 'v1', 'kind': 'Secret', 'metadata': {'name': ref['name'], 'namespace': ref['namespace']}, 'type': 'kubernetes.io/tls', 'data': {'tls.crt': base64.b64encode((tmp/'cert.pem').read_bytes()).decode(), 'tls.key': base64.b64encode((tmp/'key.pem').read_bytes()).decode()}})
        (tmp/'fixtures.yaml').write_text(yaml.safe_dump_all(fixtures))
        result = subprocess.run([args.egctl, 'x', 'translate', '--from', 'gateway-api', '--to', 'gateway-api,xds', '-f', str(tmp/'fixtures.yaml')], capture_output=True, text=True)
        if result.returncode:
            raise RuntimeError(result.stderr[:4000])
        translated = yaml.safe_load(result.stdout)
        failures = []
        def visit(value):
            if isinstance(value, dict):
                for condition in value.get('conditions', []):
                    if condition.get('type') in ['Accepted', 'ResolvedRefs', 'Programmed'] and condition.get('status') == 'False':
                        failures.append(condition)
                for child in value.values():
                    visit(child)
            elif isinstance(value, list):
                for child in value:
                    visit(child)
        visit(translated)
        assert not failures, json.dumps(failures, indent=2)
        normalized = result.stdout.lower().replace('_', '')
        for feature in ['envoy.filters.http.ext_authz', 'envoy.filters.http.cors', 'envoy.filters.http.lua', 'early_header_mutation_extensions', 'http2_protocol_options']:
            assert feature.lower().replace('_', '') in normalized, feature
    print('Offline Envoy translation passed for HTTP, gRPC, SPA, auth, and CORS fixtures. Synthetic TLS/services only; no cluster contacted.')
