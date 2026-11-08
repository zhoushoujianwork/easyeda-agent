import importlib.util
import json
import io
import os
import tarfile
from pathlib import Path
import subprocess
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / 'build-local-snapshot.py'
spec = importlib.util.spec_from_file_location('build_local_snapshot', SCRIPT)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class LocalSnapshotTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.repo = self.root / 'repo'
        self.repo.mkdir()
        subprocess.run(['git', 'init', '-q', '-b', 'dev', str(self.repo)], check=True)
        for name in ['extension/extension.json', 'extension/package.json', 'extension/package-lock.json']:
            path = self.repo / name
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(json.dumps({'version': '1.17.0', 'uuid': 'a' * 32, 'packages': {'': {'version': '1.17.0'}}}))
        (self.repo / 'extension/CHANGELOG.md').write_text('# Changelog\n')
        scripts = self.repo / 'scripts'
        scripts.mkdir()
        (scripts / 'sync-skill-version.py').write_text('import pathlib,sys\npathlib.Path("skill-version").write_text(sys.argv[1])\n')
        subprocess.run(['git', '-C', str(self.repo), 'add', '.'], check=True)
        subprocess.run(['git', '-C', str(self.repo), '-c', 'user.name=Test', '-c', 'user.email=test@example.invalid', 'commit', '-qm', 'fixture'], check=True)
        self.revision = module.git(self.repo, 'rev-parse', 'HEAD')
        for name in ['typescript', 'ts-node', 'esbuild']:
            path = self.repo / 'extension/node_modules' / name / 'package.json'
            path.parent.mkdir(parents=True)
            path.write_text('{}')
        self.out = self.root / 'out'

    def builder(self, source, stage, version):
        self.assertEqual(module.git(source, 'rev-parse', 'HEAD'), self.revision)
        self.assertIn('extension/package.json', module.git(source, 'ls-files'))
        for name in ['extension/extension.json', 'extension/package.json', 'extension/package-lock.json']:
            data = json.loads((source / name).read_text())
            self.assertEqual(data['version'], version)
            self.assertEqual(data['uuid'], 'a' * 32)
        self.assertEqual(json.loads((source / 'extension/package-lock.json').read_text())['packages']['']['version'], version)
        self.assertEqual((source / 'skill-version').read_text(), version)
        self.assertTrue((source / 'extension/node_modules').is_symlink())
        stage.mkdir()
        names = (*module.CORE[:3], 'extra-platform-cli')
        for name in names:
            (stage / name).write_text('synthetic asset')
        (stage / 'checksums.txt').write_text(''.join(f'{module.digest(stage / name)}  {name}\n' for name in names))

    def build(self, **kwargs):
        return module.build_snapshot(self.repo, kwargs.get('version', '1.17.1-dev.1'),
                                     kwargs.get('revision', self.revision), self.out,
                                     kwargs.get('builder', self.builder))

    def test_fixed_snapshot_injection_keeps_original_and_checksum_records(self):
        original = (self.repo / 'extension/package.json').read_bytes()
        # Uncommitted files do not contaminate the fixed-revision source snapshot.
        (self.repo / 'untracked-customer-note').write_text('not an input')
        result = self.build()
        self.assertEqual((self.repo / 'extension/package.json').read_bytes(), original)
        self.assertEqual(module.git(self.repo, 'rev-parse', 'HEAD'), self.revision)
        self.assertEqual(module.git(self.repo, 'branch', '--show-current'), 'dev')
        meta = json.loads((self.out / 'local-dev.json').read_text())
        self.assertEqual(meta, {'schemaVersion': 1, 'kind': 'local-dev', 'version': '1.17.1-dev.1',
                               'sourceRevision': self.revision, 'sourceBranch': 'dev',
                               'sourceDirty': False, 'versionInjected': True})
        self.assertNotIn(str(self.repo), (self.out / 'local-dev.json').read_text())
        records = dict((name, digest) for digest, name in (line.split() for line in (self.out / 'checksums.txt').read_text().splitlines()))
        self.assertIn('extra-platform-cli', records)
        self.assertNotIn('checksums.txt', records)
        self.assertEqual(records['local-dev.json'], module.digest(self.out / 'local-dev.json'))
        self.assertEqual(result['sha256']['skills.tar.gz'], module.digest(self.out / 'skills.tar.gz'))

    def test_rejects_release_versions_and_short_revision(self):
        for version in ['1.17.1', 'v1.17.1-dev.1', '1.17.1-dev.0', '1.17.1-dev.01']:
            with self.assertRaises(ValueError):
                self.build(version=version)
        with self.assertRaises(ValueError):
            self.build(revision=self.revision[:8])
        self.assertFalse(self.out.exists())

    def test_existing_output_is_never_overwritten(self):
        self.out.mkdir()
        (self.out / 'marker').write_text('keep')
        with self.assertRaisesRegex(ValueError, 'already exists'):
            self.build()
        self.assertEqual((self.out / 'marker').read_text(), 'keep')

    def test_failure_removes_staging_and_does_not_expose_partial_output(self):
        def fail(source, stage, version):
            stage.mkdir()
            (stage / 'partial').write_text('partial')
            raise ValueError('synthetic failure')
        with self.assertRaisesRegex(ValueError, 'synthetic failure'):
            self.build(builder=fail)
        self.assertFalse(self.out.exists())
        self.assertEqual(list(self.root.glob('.out-*')), [])

    def test_bad_checksum_refuses_promotion(self):
        def corrupt(source, stage, version):
            self.builder(source, stage, version)
            (stage / 'skills.tar.gz').write_text('corrupted')
        with self.assertRaisesRegex(ValueError, 'checksum'):
            self.build(builder=corrupt)
        self.assertFalse(self.out.exists())


class ArchiveExtractionTest(unittest.TestCase):
    def extract(self, entries, destination):
        archive = destination.parent / 'fixture.tar'
        with tarfile.open(archive, 'w') as stream:
            for name, kind, content in entries:
                item = tarfile.TarInfo(name)
                if kind == 'file':
                    data = content.encode()
                    item.size, item.mode = len(data), 0o755
                    stream.addfile(item, io.BytesIO(data))
                else:
                    item.type = tarfile.SYMTYPE if kind == 'symlink' else tarfile.LNKTYPE
                    item.linkname = content
                    stream.addfile(item)
        module.extract_git_archive(archive, destination)

    def test_preserves_git_files_and_necessary_relative_links(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / 'source'
            root.mkdir()
            self.extract([('AGENTS.md', 'file', 'rules'),
                          ('.agents/skills/tool', 'file', 'script'),
                          ('CLAUDE.md', 'symlink', 'AGENTS.md'),
                          ('.claude', 'symlink', '.agents'),
                          ('docs/rules', 'symlink', '../AGENTS.md')], root)
            self.assertEqual((root / 'CLAUDE.md').read_text(), 'rules')
            self.assertEqual((root / '.claude/skills/tool').read_text(), 'script')
            self.assertEqual(os.readlink(root / 'docs/rules'), '../AGENTS.md')
            self.assertTrue((root / 'AGENTS.md').stat().st_mode & 0o111)

    def test_rejects_escape_paths_links_and_special_members(self):
        cases = [('../escaped', 'file', 'bad'), ('/absolute', 'file', 'bad'),
                 ('C:/absolute', 'file', 'bad'), ('..\\escaped', 'file', 'bad'),
                 ('link', 'symlink', '../escaped'), ('link', 'symlink', '/absolute'),
                 ('link', 'symlink', 'C:\\escaped'), ('link', 'hardlink', '../escaped')]
        for entry in cases:
            with self.subTest(entry=entry), tempfile.TemporaryDirectory() as temp:
                root = Path(temp) / 'source'
                root.mkdir()
                with self.assertRaises(ValueError):
                    self.extract([entry], root)
                self.assertEqual(list(root.iterdir()), [])

    def test_rejects_symlink_parent_before_writing_files(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / 'source'
            root.mkdir()
            with self.assertRaisesRegex(ValueError, 'non-directory ancestor'):
                self.extract([('alias', 'symlink', 'internal'), ('alias/file', 'file', 'bad')], root)
            self.assertEqual(list(root.iterdir()), [])

    def test_rejects_chained_link_escape_without_external_writes(self):
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp) / 'source'
            root.mkdir()
            # Lexically internal, but a -> '.' makes b resolve outside source.
            with self.assertRaisesRegex(ValueError, 'link chain'):
                self.extract([('a', 'symlink', '.'), ('b', 'symlink', 'a/../outside')], root)
            self.assertFalse((root.parent / 'outside').exists())


if __name__ == '__main__':
    unittest.main()
