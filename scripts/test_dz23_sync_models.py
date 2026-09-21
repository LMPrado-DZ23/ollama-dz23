import importlib.util
import unittest
from unittest.mock import patch
from pathlib import Path
import tempfile
import httpx

spec = importlib.util.spec_from_file_location('sync_models', Path(__file__).with_name('dz23_sync_models.py'))
sync = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sync)

class CatalogTests(unittest.TestCase):
 def test_ollama_cloud_uses_real_ids(self):
  models = sync.parse_models('ollama-cloud', {'models': [{'name': 'deepseek-v4.1-flash'}, {'name': 'gpt-oss:120b'}]})
  self.assertEqual([x['id'] for x in models], ['deepseek-v4.1-flash', 'gpt-oss:120b'])
 def test_non_chat_and_inactive_excluded(self):
  models = sync.parse_models('groq', {'data': [{'id': 'chat'}, {'id': 'whisper-large'}, {'id': 'old', 'active': False}, {'id': 'embed-v3'}, {'id': 'canopylabs/orpheus-v1-english'}, {'id': 'gpt-realtime'}, {'id': 'gpt-image-1'}]})
  self.assertEqual([x['id'] for x in models], ['chat'])
 def test_gemini_requires_generate_content(self):
  models = sync.parse_models('gemini', {'models': [{'name': 'models/gemini-flash', 'supportedGenerationMethods': ['generateContent']}, {'name': 'models/other', 'supportedGenerationMethods': ['embedContent']}]})
  self.assertEqual(models[0]['id'], 'gemini-flash')
  self.assertEqual(len(models), 1)
 def test_no_fabricated_tools_or_coding(self):
  self.assertEqual(sync.parse_models('groq', {'data': [{'id': 'new'}]})[0]['capabilities'], ['chat'])
 def test_metadata_filters_and_capabilities(self):
  rows = [{'id': 'image-only', 'architecture': {'output_modalities': ['image']}}, {'id': 'chat', 'capabilities': {'completion_chat': True, 'function_calling': True}}]
  models = sync.parse_models('mistral', {'data': rows})
  self.assertEqual(len(models), 1)
  self.assertIn('tools', models[0]['capabilities'])
 def test_invalid_empty_catalog_is_not_success(self):
  for body in ({}, {'data': {}}, {'data': [{'id': 'bad\nname'}]}):
   with self.assertRaises(ValueError): sync.parse_models('groq', body)
 def test_auth_error_preserves_catalog_and_hides_secret(self):
  p = {'name': 'groq', 'base_url': 'https://api.groq.com/openai/v1', 'models': [{'id': 'old'}]}
  with patch.object(sync, 'credential', return_value='SECRET'), patch.object(sync, 'get_json', side_effect=ValueError('http_401')):
   result, models = sync.discover(p, {}, Path('.'))
  self.assertIsNone(models)
  self.assertEqual(result['reason'], 'http_401')
  self.assertTrue(result['previous_catalog_preserved'])
  self.assertNotIn('SECRET', str(result))
 def test_credentials_not_sent_to_changed_host(self):
  p = {'name': 'groq', 'base_url': 'https://attacker.example/v1', 'models': []}
  with patch.object(sync, 'credential', return_value='SECRET'), patch.object(sync, 'get_json') as request:
   result, _ = sync.discover(p, {}, Path('.'))
   request.assert_not_called()
  self.assertEqual(result['reason'], 'unsupported_provider_destination')
 def test_existing_priority_preserved_new_models_low_priority(self):
  p = {'name': 'groq', 'base_url': 'https://api.groq.com/openai/v1', 'models': [{'id': 'old', 'priority': 42, 'capabilities': ['chat', 'coding']}]}
  with patch.object(sync, 'credential', return_value='SECRET'), patch.object(sync, 'get_json', return_value={'data': [{'id': 'old'}, {'id': 'new'}]}):
   _, models = sync.discover(p, {}, Path('.'))
  self.assertEqual(next(m for m in models if m['id']=='old')['priority'], 42)
  self.assertEqual(next(m for m in models if m['id']=='new')['priority'], -1000)
 def test_http_status_does_not_include_upstream_body(self):
  transport = httpx.MockTransport(lambda req: httpx.Response(403, text='SECRET'))
  with httpx.Client(transport=transport) as client:
   with self.assertRaisesRegex(ValueError, '^http_403$'):
    sync.get_json(client, 'https://example.com', {})
 def test_atomic_write(self):
  with tempfile.TemporaryDirectory() as d:
   p = Path(d)/'config.json'; p.write_bytes(b'old')
   sync.atomic_write(p, b'new')
   self.assertEqual(p.read_bytes(), b'new')
   self.assertEqual(len(list(Path(d).iterdir())), 1)

if __name__ == '__main__': unittest.main()
