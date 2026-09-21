"""Synchronize configured Windows DZ23 providers. Never persist or print credentials.
Run with --apply to atomically update the model catalog; restart Ollama afterwards.
No inference requests are made. Provider errors preserve their previous catalog.
"""
import argparse
import concurrent.futures
import ctypes
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import tempfile
from urllib.parse import urlsplit
import httpx

# An explicit destination allowlist prevents configuration data redirecting secrets.
HOSTS = {
 'openai': 'api.openai.com', 'anthropic': 'api.anthropic.com',
 'gemini': 'generativelanguage.googleapis.com', 'deepseek': 'api.deepseek.com',
 'openrouter': 'openrouter.ai', 'groq': 'api.groq.com',
 'together': 'api.together.xyz', 'fireworks': 'api.fireworks.ai',
 'cerebras': 'api.cerebras.ai', 'mistral': 'api.mistral.ai', 'xai': 'api.x.ai',
 'sambanova': 'api.sambanova.ai', 'nvidia': 'integrate.api.nvidia.com',
 'novita': 'api.novita.ai', 'upstage': 'api.upstage.ai',
 'ollama-cloud': 'ollama.com', 'hyperbolic': 'api.hyperbolic.xyz',
 'alibaba': 'dashscope-intl.aliyuncs.com', 'huggingface': 'router.huggingface.co',
}
NON_CHAT = re.compile(r'embed|rerank|whisper|transcri|orpheus|realtime|audio|voxtral|gpt-image|deep-research|^text-|^babbage|^davinci|\btts\b|speech|stable.diffusion|flux|dall-e|imagen|veo-|sora|moderation|guard|reward|ocr|segmentation', re.I)
MAX_BYTES = 12 * 1024 * 1024


def user_environment():
 import winreg
 result = {}
 with winreg.OpenKey(winreg.HKEY_CURRENT_USER, 'Environment') as key:
  i = 0
  while True:
   try:
    name, value, _ = winreg.EnumValue(key, i)
    result[name] = os.path.expandvars(str(value)); i += 1
   except OSError:
    break
 return result


def unprotect(raw):
 class Blob(ctypes.Structure):
  _fields_ = [('size', ctypes.c_ulong), ('data', ctypes.POINTER(ctypes.c_ubyte))]
 cipher = bytes.fromhex(raw.decode('ascii').strip())
 buf = (ctypes.c_ubyte * len(cipher)).from_buffer_copy(cipher)
 source, dest = Blob(len(cipher), buf), Blob()
 crypt = ctypes.WinDLL('crypt32', use_last_error=True)
 kernel = ctypes.WinDLL('kernel32', use_last_error=True)
 kernel.LocalFree.argtypes = [ctypes.c_void_p]
 kernel.LocalFree.restype = ctypes.c_void_p
 if not crypt.CryptUnprotectData(ctypes.byref(source), None, None, None, None, 0, ctypes.byref(dest)):
  raise ValueError('credential_decryption_failed')
 try:
  value = ctypes.string_at(dest.data, dest.size)
  return value.decode('utf-16-le' if len(value) > 1 and value[1] == 0 else 'utf-8').strip('\0 \r\n')
 finally:
  ctypes.memset(dest.data, 0, dest.size)
  kernel.LocalFree(dest.data)


def credential(provider, env, root):
 name = provider.get('api_key_env', '')
 if not re.fullmatch(r'[A-Z][A-Z0-9_]{0,127}', name):
  return ''
 if env.get(name):
  return env[name].strip()
 path = Path(env.get(name + '_FILE') or root / 'credentials' / (name + '.dpapi'))
 if not path.is_file():
  return ''
 if path.stat().st_size > 65536:
  raise ValueError('credential_file_too_large')
 return unprotect(path.read_bytes()) if path.suffix.lower() == '.dpapi' else path.read_text().strip()


def parse_models(name, body):
 rows = body.get('models' if name in ('gemini', 'ollama-cloud') else 'data', [])
 if not isinstance(rows, list):
  raise ValueError('invalid_catalog_shape')
 result = {}
 for row in rows:
  if not isinstance(row, dict):
   continue
  mid = row.get('name') if name in ('gemini', 'ollama-cloud') else row.get('id')
  if name == 'gemini' and isinstance(mid, str):
   mid = mid.removeprefix('models/')
  if not isinstance(mid, str) or not mid.strip() or len(mid) > 512 or re.search(r'[\x00-\x1f\x7f]', mid):
   continue
  if row.get('active') is False or NON_CHAT.search(mid):
   continue
  if name == 'gemini' and 'generateContent' not in row.get('supportedGenerationMethods', []):
   continue
  kind = str(row.get('type', '')).lower()
  if kind in ('embedding', 'image', 'audio', 'rerank', 'text-to-image'):
   continue
  caps = row.get('capabilities') or {}
  if isinstance(caps, dict) and caps.get('completion_chat') is False:
   continue
  arch = row.get('architecture') or {}
  if isinstance(arch, dict) and arch.get('output_modalities') and 'text' not in arch['output_modalities']:
   continue
  capabilities = ['chat']
  if isinstance(caps, dict):
   if caps.get('function_calling') is True: capabilities.append('tools')
   if caps.get('vision') is True: capabilities.append('vision')
  if 'tools' in row.get('supported_parameters', []): capabilities.append('tools')
  result[mid] = {'id': mid, 'capabilities': capabilities, 'priority': -1000}
 if not result:
  raise ValueError('no_chat_models_returned')
 return list(result.values())


def get_json(client, url, headers, params=None):
 with client.stream('GET', url, headers=headers, params=params) as response:
  if response.status_code != 200:
   raise ValueError('http_' + str(response.status_code))
  chunks, size = [], 0
  for chunk in response.iter_bytes():
   size += len(chunk)
   if size > MAX_BYTES: raise ValueError('catalog_too_large')
   chunks.append(chunk)
  result = json.loads(b''.join(chunks))
  if not isinstance(result, dict): raise ValueError('invalid_catalog_shape')
  return result


def discover(provider, env, root):
 name = provider['name']
 status = {'provider': name, 'previous_models': len(provider.get('models', []))}
 try:
  if provider.get('enabled') is False:
   return status | {'status': 'disabled'}, None
  key = credential(provider, env, root)
  status['credential_present'] = bool(key)
  if not key:
   return status | {'status': 'missing_credential'}, None
  base = provider['base_url'].rstrip('/')
  parsed = urlsplit(base)
  if parsed.scheme != 'https' or parsed.hostname != HOSTS.get(name) or parsed.username or parsed.port not in (None, 443):
   raise ValueError('unsupported_provider_destination')
  headers = {'Authorization': 'Bearer ' + key}
  url, params = base + '/models', {}
  if name == 'ollama-cloud': url = 'https://ollama.com/api/tags'
  if name == 'gemini':
   url = 'https://generativelanguage.googleapis.com/v1beta/models'
   headers = {'x-goog-api-key': key}; params = {'pageSize': 1000}
  if name == 'anthropic':
   headers = {'x-api-key': key, 'anthropic-version': '2023-06-01'}; params = {'limit': 1000}
  merged = {}
  with httpx.Client(timeout=25, follow_redirects=False, trust_env=False) as client:
   for _ in range(20):
    body = get_json(client, url, headers, params)
    for model in parse_models(name, body): merged[model['id']] = model
    if name == 'gemini' and body.get('nextPageToken'):
     params['pageToken'] = body['nextPageToken']; continue
    if name == 'anthropic' and body.get('has_more') and body.get('last_id'):
     params['after_id'] = body['last_id']; continue
    break
   else:
    raise ValueError('pagination_limit')
  # Preserve capability overrides and routing priority of existing models.
  old = {m['id']: m for m in provider.get('models', [])}
  for mid in merged:
   if mid in old: merged[mid] = old[mid]
  # Explicit routing aliases may be absent from the provider's generic catalog.
  for mid, model in old.items():
   if mid in ('openrouter/free',) or mid.endswith(':fastest'):
    merged.setdefault(mid, model)
  models = sorted(merged.values(), key=lambda m: m['id'])
  return status | {'status': 'catalog_synced', 'models': len(models), 'inference_tested': False}, models
 except Exception as exc:
  # Never expose HTTP error bodies, URLs with credentials or exception messages.
  label = str(exc) if isinstance(exc, ValueError) and re.fullmatch(r'[a-z_0-9]+', str(exc)) else type(exc).__name__
  return status | {'status': 'blocked', 'reason': label, 'previous_catalog_preserved': True}, None


def atomic_write(path, data):
 fd, tmp = tempfile.mkstemp(prefix='.sync-', dir=path.parent)
 try:
  with os.fdopen(fd, 'wb') as stream:
   stream.write(data); stream.flush(); os.fsync(stream.fileno())
  os.replace(tmp, path)
 finally:
  if os.path.exists(tmp): os.unlink(tmp)


def main():
 parser = argparse.ArgumentParser()
 parser.add_argument('--apply', action='store_true')
 parser.add_argument('--config', type=Path)
 args = parser.parse_args()
 env = dict(os.environ); env.update(user_environment())
 root = Path(os.environ['APPDATA']) / 'Ollama DZ23'
 path = args.config or Path(env.get('OLLAMA_DZ23_CONFIG') or root / 'dz23-providers.json')
 original = path.read_bytes(); config = json.loads(original.decode('utf-8-sig'))
 report = {'timestamp': dt.datetime.now(dt.timezone.utc).isoformat(), 'applied': False, 'providers': []}
 with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
  results = list(pool.map(lambda p: discover(p, env, root), config['providers']))
 for provider, (status, models) in zip(config['providers'], results):
  report['providers'].append(status)
  if models is not None: provider['models'] = models
  print(json.dumps(status), flush=True)
 updated = (json.dumps(config, ensure_ascii=False, indent=2) + '\n').encode('utf-8')
 if args.apply and config != json.loads(original.decode('utf-8-sig')):
  if path.read_bytes() != original: raise RuntimeError('Configuration changed during sync; rerun to preserve edits')
  backup = path.with_name(path.name + '.backup-sync-' + dt.datetime.now().strftime('%Y%m%d-%H%M%S'))
  shutil.copy2(path, backup)
  atomic_write(path, updated)
  report.update(applied=True, backup=str(backup), restart_required=True)
 report['config_sha256'] = hashlib.sha256(path.read_bytes()).hexdigest()
 out = Path(__file__).resolve().parent / 'sync-status.json'
 atomic_write(out, (json.dumps(report, indent=2)+'\n').encode())
 print(json.dumps({'applied': report['applied'], 'report': str(out)}))

if __name__ == '__main__':
 main()
