import { describe, expect, it } from 'vitest'
import type { StoryValidationContent } from '../src/index'
import { hasValidationErrors, validateStoryContent } from '../src/index'

const validContent: StoryValidationContent = {
  story: {
    storyId: 'midnight-cafe',
    startEpisodeId: 'ep_001',
    characters: ['luna'],
    variables: {
      lunaTrust: 0
    },
    episodes: [
      {
        id: 'ep_001',
        titleKey: 'ep_001.title',
        sceneIds: ['scene_001']
      }
    ]
  },
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
              voice: 'voices/en-US/luna/ep_001_n001.mp3',
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
                  effects: {
                    lunaTrust: 1
                  }
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
  targetLocale: {
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
  nativeLocale: {
    locale: 'zh-CN',
    entries: {
      'ep_001.title': '雨夜',
      'ep_001.cliffhanger': 'Luna 说她以前见过你。',
      'line.ep001.n001': '你今天看起来很累。',
      'choice.ep001.n002.prompt': '你要怎么回答？',
      'choice.ep001.n002.c1': '我累了。',
      'ep_001.ending': '雨还在下。'
    }
  },
  learningItems: [
    {
      id: 'expr_you_look_tired_today',
      type: 'expression',
      targetLanguage: 'en-US',
      text: 'You look tired today.',
      translation: '你今天看起来很累。',
      tokens: [],
      examples: []
    }
  ],
  assets: [
    {
      assetKey: 'bg_cafe_rainy_night'
    },
    {
      assetKey: 'luna.neutral'
    },
    {
      assetKey: 'voices/en-US/luna/ep_001_n001.mp3'
    }
  ]
}

describe('validator', () => {
  it('accepts a connected story with matching text, learning items, and assets', () => {
    const diagnostics = validateStoryContent(validContent)

    expect(hasValidationErrors(diagnostics)).toBe(false)
  })

  it('reports broken links and missing content references', () => {
    const invalidContent = structuredClone(validContent)
    const episode = invalidContent.episodes[0]
    const scene = episode.scenes[0]
    const choice = scene.nodes.find((node) => node.type === 'choice')
    const dialogue = scene.nodes.find((node) => node.type === 'dialogue')

    if (!choice || choice.type !== 'choice' || !dialogue || dialogue.type !== 'dialogue') {
      throw new Error('Fixture shape changed')
    }

    choice.choices[0].next = 'missing_node'
    dialogue.textKey = 'line.ep001.missing'
    dialogue.learningPointIds = ['expr_missing']
    dialogue.sprite = 'luna.missing'
    invalidContent.nativeLocale = {
      locale: 'zh-CN',
      entries: {}
    }

    const diagnostics = validateStoryContent(invalidContent)
    const codes = diagnostics.map((diagnostic) => diagnostic.code)

    expect(codes).toContain('MISSING_NEXT_NODE')
    expect(codes).toContain('MISSING_TARGET_TEXT_KEY')
    expect(codes).toContain('MISSING_NATIVE_TEXT_KEY')
    expect(codes).toContain('MISSING_LEARNING_ITEM')
    expect(codes).toContain('MISSING_ASSET')
    expect(hasValidationErrors(diagnostics)).toBe(true)
  })

  it('warns on unreachable nodes and too many learning points', () => {
    const noisyContent = structuredClone(validContent)
    const episode = noisyContent.episodes[0]
    const scene = episode.scenes[0]

    episode.targetExpressionIds = Array.from({ length: 9 }, (_, index) => `expr_${index}`)
    noisyContent.learningItems = episode.targetExpressionIds.map((id) => ({
      id,
      type: 'expression',
      targetLanguage: 'en-US',
      text: id,
      tokens: [],
      examples: []
    }))
    scene.nodes.push({
      id: 'n999',
      type: 'dialogue',
      speaker: 'luna',
      textKey: 'line.ep001.n001',
      learningPointIds: [],
      next: 'n003'
    })

    const diagnostics = validateStoryContent(noisyContent)
    const codes = diagnostics.map((diagnostic) => diagnostic.code)

    expect(codes).toContain('UNREACHABLE_NODE')
    expect(codes).toContain('TOO_MANY_LEARNING_POINTS')
  })
})
