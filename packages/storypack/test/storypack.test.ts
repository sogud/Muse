import { describe, expect, it } from 'vitest'
import {
  CharacterDefinitionSchema,
  EpisodeDefinitionSchema,
  LearningBundleSchema,
  LocaleBundleSchema,
  ProjectManifestSchema,
  StoryDefinitionSchema
} from '@aivn/story-schema'
import type { BuildStoryPackInput } from '../src/index'
import { StoryPackBuildError, buildStoryPack, validateForStoryPack } from '../src/index'
import lunaJson from '../../../test/fixtures/midnight-cafe/characters/luna.json'
import ep001Json from '../../../test/fixtures/midnight-cafe/episodes/ep_001.json'
import ep002Json from '../../../test/fixtures/midnight-cafe/episodes/ep_002.json'
import ep003Json from '../../../test/fixtures/midnight-cafe/episodes/ep_003.json'
import learningEnJson from '../../../test/fixtures/midnight-cafe/learning/en-US.json'
import learningZhJson from '../../../test/fixtures/midnight-cafe/learning/zh-CN.json'
import localeEnJson from '../../../test/fixtures/midnight-cafe/locales/en-US.json'
import localeZhJson from '../../../test/fixtures/midnight-cafe/locales/zh-CN.json'
import projectJson from '../../../test/fixtures/midnight-cafe/project.json'
import storyJson from '../../../test/fixtures/midnight-cafe/story.json'

const validInput: BuildStoryPackInput = {
  project: {
    projectId: 'midnight-cafe',
    title: {
      'en-US': 'Midnight Cafe',
      'zh-CN': '午夜咖啡馆'
    },
    version: '0.1.0',
    engineVersion: '0.1.0',
    targetLanguage: 'en-US',
    nativeLanguage: 'zh-CN',
    level: 'A2',
    contentRating: 'teen',
    createdAt: '2026-05-24T00:00:00.000Z',
    updatedAt: '2026-05-24T00:00:00.000Z'
  },
  story: {
    storyId: 'midnight-cafe',
    startEpisodeId: 'ep_001',
    characters: ['luna'],
    variables: {},
    episodes: [
      {
        id: 'ep_001',
        titleKey: 'ep_001.title',
        sceneIds: ['scene_001']
      }
    ]
  },
  characters: [
    {
      id: 'luna',
      name: 'Luna',
      role: 'cafe_barista',
      personality: ['calm'],
      speakingStyle: {},
      relationshipArc: [],
      visualDesign: {},
      sprites: {
        neutral: {
          id: 'luna.neutral'
        }
      }
    }
  ],
  episodes: [
    {
      episodeId: 'ep_001',
      titleKey: 'ep_001.title',
      cliffhangerKey: 'ep_001.cliffhanger',
      targetExpressionIds: ['expr_you_look_tired_today'],
      scenes: [
        {
          id: 'scene_001',
          background: 'bg_cafe_rainy_night',
          startNodeId: 'n001',
          nodes: [
            {
              id: 'n001',
              type: 'dialogue',
              speaker: 'luna',
              textKey: 'line.ep001.n001',
              sprite: 'luna.neutral',
              learningPointIds: ['expr_you_look_tired_today'],
              next: 'n002'
            },
            {
              id: 'n002',
              type: 'choice',
              promptKey: 'choice.ep001.n002.prompt',
              choices: [
                {
                  id: 'c1',
                  textKey: 'choice.ep001.n002.c1',
                  next: 'n003',
                  effects: {}
                }
              ]
            },
            {
              id: 'n003',
              type: 'end',
              endingKey: 'ep_001.ending'
            }
          ]
        }
      ]
    }
  ],
  locales: [
    {
      locale: 'en-US',
      entries: {
        'ep_001.title': 'A Rainy Night',
        'ep_001.cliffhanger': 'Luna says she has seen you before.',
        'line.ep001.n001': 'You look tired today.',
        'choice.ep001.n002.prompt': 'How do you reply?',
        'choice.ep001.n002.c1': "I'm tired.",
        'ep_001.ending': 'The rain keeps falling.'
      }
    },
    {
      locale: 'zh-CN',
      entries: {
        'ep_001.title': '雨夜',
        'ep_001.cliffhanger': 'Luna 说她以前见过你。',
        'line.ep001.n001': '你今天看起来很累。',
        'choice.ep001.n002.prompt': '你要怎么回答？',
        'choice.ep001.n002.c1': '我累了。',
        'ep_001.ending': '雨还在下。'
      }
    }
  ],
  learning: [
    {
      targetLanguage: 'en-US',
      items: [
        {
          id: 'expr_you_look_tired_today',
          type: 'expression',
          targetLanguage: 'en-US',
          text: 'You look tired today.',
          tokens: [],
          examples: []
        }
      ]
    }
  ],
  assets: [
    {
      assetKey: 'bg_cafe_rainy_night',
      assetType: 'background'
    },
    {
      assetKey: 'luna.neutral',
      assetType: 'character_sprite'
    }
  ]
}

describe('storypack', () => {
  it('builds a JSON StoryPack with an entry and checksums', () => {
    const result = buildStoryPack(validInput)

    expect(result.storypack.manifest.entry).toEqual({
      episodeId: 'ep_001',
      sceneId: 'scene_001',
      nodeId: 'n001'
    })
    expect(result.storypack.manifest.assetIndex).toBe('assets.json')
    expect(result.storypack.checksums['story.json']).toMatch(/^[0-9a-f]{8}$/)
  })

  it('returns diagnostics for missing locales', () => {
    const invalidInput = structuredClone(validInput)
    invalidInput.locales = invalidInput.locales.filter((locale) => locale.locale !== 'zh-CN')

    const diagnostics = validateForStoryPack(invalidInput)

    expect(diagnostics.map((diagnostic) => diagnostic.code)).toContain('MISSING_NATIVE_LOCALE')
  })

  it('throws when validation has errors', () => {
    const invalidInput = structuredClone(validInput)
    invalidInput.episodes[0].scenes[0].nodes[0] = {
      id: 'n001',
      type: 'dialogue',
      speaker: 'luna',
      textKey: 'line.ep001.missing',
      learningPointIds: [],
      next: 'n002'
    }

    expect(() => buildStoryPack(invalidInput)).toThrow(StoryPackBuildError)
  })

  it('builds the handwritten three-episode Midnight Cafe demo', () => {
    const input: BuildStoryPackInput = {
      project: ProjectManifestSchema.parse(projectJson),
      story: StoryDefinitionSchema.parse(storyJson),
      characters: [CharacterDefinitionSchema.parse(lunaJson)],
      episodes: [ep001Json, ep002Json, ep003Json].map((episode) =>
        EpisodeDefinitionSchema.parse(episode)
      ),
      locales: [
        LocaleBundleSchema.parse(localeEnJson),
        LocaleBundleSchema.parse(localeZhJson)
      ],
      learning: [
        LearningBundleSchema.parse(learningEnJson),
        LearningBundleSchema.parse(learningZhJson)
      ],
      assets: [
        { assetKey: 'bg_cafe_rainy_night', assetType: 'background' },
        { assetKey: 'bg_cafe_counter', assetType: 'background' },
        { assetKey: 'bg_cafe_empty_table', assetType: 'background' },
        { assetKey: 'bgm_soft_rain', assetType: 'bgm' },
        { assetKey: 'luna.neutral', assetType: 'character_sprite' },
        { assetKey: 'luna.smile', assetType: 'character_sprite' },
        { assetKey: 'luna.sad', assetType: 'character_sprite' },
        { assetKey: 'luna.surprised', assetType: 'character_sprite' },
        { assetKey: 'voices/en-US/luna/ep_001_n002.mp3', assetType: 'voice' },
        { assetKey: 'voices/en-US/luna/ep_001_n008.mp3', assetType: 'voice' },
        { assetKey: 'voices/en-US/luna/ep_001_n009.mp3', assetType: 'voice' },
        { assetKey: 'voices/en-US/luna/ep_001_n010.mp3', assetType: 'voice' },
        { assetKey: 'voices/en-US/luna/ep_002_n002.mp3', assetType: 'voice' },
        { assetKey: 'voices/en-US/luna/ep_002_n007.mp3', assetType: 'voice' },
        { assetKey: 'voices/en-US/luna/ep_002_n009.mp3', assetType: 'voice' },
        { assetKey: 'voices/en-US/luna/ep_003_n002.mp3', assetType: 'voice' },
        { assetKey: 'voices/en-US/luna/ep_003_n003.mp3', assetType: 'voice' },
        { assetKey: 'voices/en-US/luna/ep_003_n008.mp3', assetType: 'voice' },
        { assetKey: 'voices/en-US/luna/ep_003_n009.mp3', assetType: 'voice' }
      ]
    }

    const result = buildStoryPack(input)

    expect(result.diagnostics.filter((diagnostic) => diagnostic.severity === 'error')).toEqual([])
    expect(result.storypack.episodes).toHaveLength(3)
    expect(result.storypack.learning[0]?.items).toHaveLength(15)
  })
})
